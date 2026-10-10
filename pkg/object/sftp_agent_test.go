//go:build !nosftp

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package object

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type failingSftpAgent struct{ agent.Agent }

func (f failingSftpAgent) List() ([]*agent.Key, error) {
	return nil, errors.New("injected agent list failure")
}

func TestSftpAgentConnection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix agent socket")
	}
	for _, failure := range []bool{true, false} {
		name := "signing"
		if failure {
			name = "list_failure"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SSH_PRIVATE_KEY_PATH", "")
			t.Setenv("SSH_KNOWN_HOSTS", "")
			pub, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			publicKey, err := ssh.NewPublicKey(pub)
			if err != nil {
				t.Fatal(err)
			}
			keyring := agent.NewKeyring()
			if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
				t.Fatal(err)
			}
			var backend agent.Agent = keyring
			if failure {
				backend = failingSftpAgent{backend}
			}
			agentListener, err := net.Listen("unix", filepath.Join(home, "agent.sock"))
			if err != nil {
				t.Fatal(err)
			}
			agentConn := make(chan net.Conn, 1)
			agentDone := make(chan error, 1)
			agentFinished := make(chan struct{})
			t.Cleanup(func() {
				_ = agentListener.Close()
				select {
				case <-agentFinished:
				case <-time.After(3 * time.Second):
					t.Error("agent server did not stop")
				}
			})
			go func() {
				defer close(agentFinished)
				c, err := agentListener.Accept()
				if err != nil {
					agentDone <- err
					return
				}
				agentConn <- c
				agentDone <- agent.ServeAgent(backend, c)
				_ = c.Close()
			}()
			t.Setenv("SSH_AUTH_SOCK", agentListener.Addr().String())
			endpoint := startSftpAgentTestServer(t, publicKey, failure)
			store, err := newSftp(endpoint, "test-user", "test-password", "")
			var accepted net.Conn
			select {
			case accepted = <-agentConn:
				t.Cleanup(func() { _ = accepted.Close() })
			case <-time.After(3 * time.Second):
				t.Fatal("constructor did not query the agent")
			}
			if err != nil {
				t.Fatal(err)
			}
			f := store.(*sftpStore)
			t.Cleanup(func() {
				for _, c := range f.pool {
					_ = c.close()
				}
			})
			if failure {
				select {
				case err := <-agentDone:
					if !errors.Is(err, io.EOF) {
						t.Fatalf("agent ended with %v, want peer EOF", err)
					}
				case <-time.After(time.Second):
					t.Fatal("failed agent query left its socket open after successful password fallback")
				}
			} else {
				select {
				case err := <-agentDone:
					t.Fatalf("agent socket needed by signers was closed: %v", err)
				default:
				}
			}
		})
	}
}

func startSftpAgentTestServer(t *testing.T, publicKey ssh.PublicKey, password bool) string {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{}
	if password {
		config.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "test-user" && string(pass) == "test-password" {
				return nil, nil
			}
			return nil, errors.New("invalid password")
		}
	} else {
		config.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "test-user" && bytes.Equal(publicKey.Marshal(), key.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("invalid public key")
		}
	}
	config.AddHostKey(hostKey)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("SSH/SFTP server did not stop")
		}
	})
	go func() {
		defer close(done)
		c, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- c
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		connection, channels, requests, err := ssh.NewServerConn(c, config)
		if err != nil {
			return
		}
		defer connection.Close()
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			if channel.ChannelType() != "session" {
				_ = channel.Reject(ssh.UnknownChannelType, "session required")
				continue
			}
			stream, reqs, err := channel.Accept()
			if err != nil {
				return
			}
			for request := range reqs {
				var payload struct{ Name string }
				ok := request.Type == "subsystem" && ssh.Unmarshal(request.Payload, &payload) == nil && payload.Name == "sftp"
				_ = request.Reply(ok, nil)
				if ok {
					server := sftp.NewRequestServer(stream, sftp.InMemHandler())
					_ = server.Serve()
					_ = server.Close()
					return
				}
			}
		}
	}()
	return listener.Addr().String() + ":/"
}
