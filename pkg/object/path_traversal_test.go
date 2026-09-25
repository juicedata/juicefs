package object

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func sftpTestServer(listener net.Listener, config *ssh.ServerConfig) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			sconn, chans, reqs, err := ssh.NewServerConn(c, config)
			if err != nil {
				return
			}
			defer sconn.Close()
			go ssh.DiscardRequests(reqs)
			for newCh := range chans {
				if newCh.ChannelType() != "session" {
					newCh.Reject(ssh.UnknownChannelType, "unknown")
					continue
				}
				ch, reqs, err := newCh.Accept()
				if err != nil {
					continue
				}
				go func() {
					for req := range reqs {
						if req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp" {
							req.Reply(true, nil)
							srv, _ := sftp.NewServer(ch)
							srv.Serve()
							return
						}
						req.Reply(false, nil)
					}
				}()
			}
		}(conn)
	}
}

func TestSftpStoreRejectsKeyPathTraversal(t *testing.T) {
	workdir := t.TempDir()
	sftpRoot := filepath.Join(workdir, "safe", "root") + "/"
	os.MkdirAll(sftpRoot, 0755)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	sshConfig := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	sshConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	go sftpTestServer(listener, sshConfig)

	endpoint := fmt.Sprintf("127.0.0.1:%d:%s", port, sftpRoot)
	store, err := newSftp(endpoint, "test", "test", "")
	if err != nil {
		t.Fatal(err)
	}

	PutInplace = true

	traversalKeys := []string{
		"../../pwned.txt",
		"../outside",
		"dir/../../pwned.txt",
		"/../../../etc/passwd",
	}

	for _, key := range traversalKeys {
		if err := store.Put(context.Background(), key, bytes.NewReader([]byte("ESCAPED"))); err == nil {
			t.Fatalf("Put(%q) should have been rejected", key)
		}
	}

	escaped := filepath.Join(workdir, "pwned.txt")
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("file escaped storage root: %s", escaped)
	}

	for _, key := range traversalKeys {
		if _, err := store.Head(context.Background(), key); err == nil {
			t.Fatalf("Head(%q) should have been rejected", key)
		}
	}

	validKeys := []string{
		"normal.txt",
		"subdir/file.txt",
		"a/b/c.txt",
	}
	for _, key := range validKeys {
		if err := store.Put(context.Background(), key, bytes.NewReader([]byte("OK"))); err != nil {
			t.Fatalf("Put(%q) should succeed: %v", key, err)
		}
	}
}
