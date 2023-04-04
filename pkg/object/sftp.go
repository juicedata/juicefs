//go:build !nosftp
// +build !nosftp

// Part of this file is borrowed from Rclone under MIT license:
// https://github.com/ncw/rclone/blob/master/backend/sftp/sftp.go

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

// conn encapsulates an ssh client and corresponding sftp client
type conn struct {
	sshClient  *ssh.Client
	sftpClient *sftp.Client
	err        chan error
}

// Wait for connection to close
func (c *conn) wait() {
	c.err <- c.sshClient.Conn.Wait()
}

// Closes the connection
func (c *conn) close() error {
	sftpErr := c.sftpClient.Close()
	sshErr := c.sshClient.Close()
	if sftpErr != nil {
		return sftpErr
	}
	return sshErr
}

// Returns an error if closed
func (c *conn) closed() error {
	select {
	case err := <-c.err:
		return err
	default:
	}
	return nil
}

type sftpStore struct {
	DefaultObjectStorage
	host   string
	port   string
	root   string
	config *ssh.ClientConfig
	poolMu sync.Mutex
	pool   []*conn
}

// Open a new connection to the SFTP server.
func (f *sftpStore) sftpConnection() (c *conn, err error) {
	c = &conn{
		err: make(chan error, 1),
	}
	conn, err := net.Dial("tcp", net.JoinHostPort(f.host, f.port))
	if err != nil {
		return nil, err
	}
	sshc, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(f.host, f.port), f.config)
	if err != nil {
		return nil, err
	}
	c.sshClient = ssh.NewClient(sshc, chans, reqs)
	c.sftpClient, err = sftp.NewClient(c.sshClient)
	if err != nil {
		_ = c.sshClient.Close()
		return nil, errors.Wrap(err, "couldn't initialise SFTP")
	}
	go c.wait()
	return c, nil
}

// Get an SFTP connection from the pool, or open a new one
func (f *sftpStore) getSftpConnection() (c *conn, err error) {
	f.poolMu.Lock()
	for len(f.pool) > 0 {
		c = f.pool[0]
		f.pool = f.pool[1:]
		err := c.closed()
		if err == nil {
			break
		}
		c = nil
	}
	f.poolMu.Unlock()
	if c != nil {
		return c, nil
	}
	return f.sftpConnection()
}

// Return an SFTP connection to the pool
//
// It nils the pointed to connection out so it can't be reused
//
// if err is not nil then it checks the connection is alive using a
// Getwd request
func (f *sftpStore) putSftpConnection(pc **conn, err error) {
	c := *pc
	*pc = nil
	if err != nil {
		// work out if this is an expected error
		underlyingErr := errors.Cause(err)
		isRegularError := false
		switch underlyingErr {
		case os.ErrNotExist:
			isRegularError = true
		default:
			switch underlyingErr.(type) {
			case *sftp.StatusError, *os.PathError:
				isRegularError = true
			}
		}
		// If not a regular SFTP error code then check the connection
		if !isRegularError {
			_, nopErr := c.sftpClient.Getwd()
			if nopErr != nil {
				_ = c.close()
				return
			}
		}
	}
	f.poolMu.Lock()
	f.pool = append(f.pool, c)
	f.poolMu.Unlock()
}

func (f *sftpStore) String() string {
	return fmt.Sprintf("%s@%s:%s", f.config.User, f.host, f.root)
}

// always preserve suffix `/` for directory key
func (f *sftpStore) path(key string) string {
	return f.root + key
}

func (f *sftpStore) Head(key string) (Object, error) {
	c, err := f.getSftpConnection()
	if err != nil {
		return nil, err
	}
	defer f.putSftpConnection(&c, err)

	info, err := c.sftpClient.Stat(f.path(key))
	if err != nil {
		return nil, err
	}
	return f.fileInfo(nil, key, info), nil
}

func (f *sftpStore) Get(key string, off, limit int64) (io.ReadCloser, error) {
	c, err := f.getSftpConnection()
	if err != nil {
		return nil, err
	}
	defer f.putSftpConnection(&c, err)

	p := f.path(key)
	ff, err := c.sftpClient.Open(p)
	if err != nil {
		return nil, err
	}
	finfo, err := ff.Stat()
	if err != nil {
		return nil, err
	}
	if finfo.IsDir() {
		return ioutil.NopCloser(bytes.NewBuffer([]byte{})), nil
	}

	if limit > 0 {
		return &SectionReaderCloser{
			SectionReader: io.NewSectionReader(ff, off, limit),
			Closer:        ff,
		}, nil
	}
	return ff, err
}

func (f *sftpStore) Put(key string, in io.Reader) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)

	p := f.path(key)
	if strings.HasSuffix(p, dirSuffix) {
		return c.sftpClient.MkdirAll(p)
	}
	if err := c.sftpClient.MkdirAll(filepath.Dir(p)); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".tmp")
	if runtime.GOOS == "windows" {
		tmp = strings.Replace(tmp, "\\", "/", -1)
	}

	ff, err := c.sftpClient.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer func() { _ = c.sftpClient.Remove(tmp) }()
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	_, err = io.CopyBuffer(ff, in, *buf)
	if err != nil {
		_ = ff.Close()
		return err
	}
	err = ff.Close()
	if err != nil {
		return err
	}
	_ = c.sftpClient.Remove(p)
	return c.sftpClient.Rename(tmp, p)
}

func (f *sftpStore) Chtimes(key string, mtime time.Time) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)
	return c.sftpClient.Chtimes(f.path(key), mtime, mtime)
}

func (f *sftpStore) Chmod(key string, mode os.FileMode) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)
	return c.sftpClient.Chmod(f.path(key), mode)
}

func (f *sftpStore) Chown(key string, owner, group string) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)
	uid := lookupUser(owner)
	gid := lookupGroup(group)
	return c.sftpClient.Chown(f.path(key), uid, gid)
}

func (f *sftpStore) Symlink(oldName, newName string) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)
	p := f.path(newName)
	err = c.sftpClient.Symlink(oldName, p)
	if err != nil && os.IsNotExist(err) {
		_ = c.sftpClient.MkdirAll(filepath.Dir(p))
		err = c.sftpClient.Symlink(oldName, p)
	}
	return err
}

func (f *sftpStore) Readlink(name string) (string, error) {
	c, err := f.getSftpConnection()
	if err != nil {
		return "", err
	}
	defer f.putSftpConnection(&c, err)
	return c.sftpClient.ReadLink(f.path(name))
}

func (f *sftpStore) Delete(key string) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)
	err = c.sftpClient.Remove(strings.TrimRight(f.path(key), dirSuffix))
	if err != nil && os.IsNotExist(err) {
		err = nil
	}
	return err
}

func (f *sftpStore) sortByName(c *sftp.Client, path string, fis []os.FileInfo) []Object {
	var obs = make([]Object, 0, len(fis))
	for _, fi := range fis {
		p := path + fi.Name()
		if strings.HasPrefix(p, f.root) {
			key := p[len(f.root):]
			obs = append(obs, f.fileInfo(c, key, fi))
		}
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].Key() < obs[j].Key() })
	return obs
}

func (f *sftpStore) fileInfo(c *sftp.Client, key string, fi os.FileInfo) Object {
	owner, group := getOwnerGroup(fi)
	isSymlink := !fi.Mode().IsDir() && !fi.Mode().IsRegular()
	if isSymlink && c != nil {
		if fi2, err := c.Stat(f.root + key); err == nil {
			fi = fi2
		}
	}
	ff := &file{
		obj{key, fi.Size(), fi.ModTime(), fi.IsDir()},
		owner,
		group,
		fi.Mode(),
		isSymlink,
	}
	if fi.IsDir() {
		if key != "" && !strings.HasSuffix(key, "/") {
			ff.key += "/"
		}
		ff.size = 0
	}
	return ff
}

func (f *sftpStore) doFind(c *sftp.Client, path, marker string, out chan Object) {
	infos, err := c.ReadDir(path)
	if err != nil {
		logger.Errorf("readdir %s: %s", path, err)
		return
	}

	obs := f.sortByName(c, path, infos)
	for _, o := range obs {
		key := o.Key()
		if key > marker {
			out <- o
		}
		if o.IsDir() && (key > marker || strings.HasPrefix(marker, key)) {
			f.doFind(c, f.root+key, marker, out)
		}
	}
}

func (f *sftpStore) find(c *sftp.Client, path, marker string, out chan Object) {
	if strings.HasSuffix(path, dirSuffix) {
		fi, err := c.Stat(path)
		if err != nil {
			logger.Errorf("Stat %s error: %q", path, err)
			return
		}
		if marker == "" {
			out <- f.fileInfo(nil, path[len(f.root):], fi)
		}
		f.doFind(c, path, marker, out)
	} else {
		// As files or dirs in the same directory of file `path` resides
		// may have prefix `path`, we should list the directory.
		dir := filepath.Dir(path) + dirSuffix
		infos, err := c.ReadDir(dir)
		if err != nil {
			logger.Errorf("readdir %s: %s", dir, err)
			return
		}

		obs := f.sortByName(c, dir, infos)
		for _, o := range obs {
			key := o.Key()
			p := f.root + o.Key()
			if strings.HasPrefix(p, path) {
				if key > marker || marker == "" {
					out <- o
				}
				if o.IsDir() && (key > marker || strings.HasPrefix(marker, key)) {
					f.doFind(c, p, marker, out)
				}
			}
		}
	}
}

func (f *sftpStore) List(prefix, marker string, limit int64) ([]Object, error) {
	return nil, notSupported
}

func (f *sftpStore) ListAll(prefix, marker string) (<-chan Object, error) {
	c, err := f.getSftpConnection()
	if err != nil {
		return nil, err
	}
	listed := make(chan Object, 10240)
	go func() {
		defer f.putSftpConnection(&c, nil)

		f.find(c.sftpClient, f.path(prefix), marker, listed)
		close(listed)
	}()
	return listed, nil
}

func SshInteractive(user, instruction string, questions []string, echos []bool) (answers []string, err error) {
	if len(questions) == 0 {
		fmt.Print(user, instruction)
	} else {
		answers = make([]string, len(questions))
		for i, q := range questions {
			fmt.Print(q)
			var ans []byte
			if echos[i] {
				_, err = fmt.Scanln(&answers[i])
			} else {
				ans, err = term.ReadPassword(int(syscall.Stdin))
				answers[i] = string(ans)
			}
			if err != nil {
				return nil, fmt.Errorf("read password: %s", err)
			}
		}
	}
	return answers, nil
}
func unescape(original string) string {
	if escaped, err := url.QueryUnescape(original); err != nil {
		logger.Warnf("unescape(%s) error: %s", original, err)
		return original
	} else {
		return escaped
	}
}

func newSftp(endpoint, username, pass, token string) (ObjectStorage, error) {
	idx := strings.LastIndex(endpoint, ":")
	host, port, err := net.SplitHostPort(endpoint[:idx])
	if err != nil && strings.Contains(err.Error(), "missing port") {
		host, port, err = net.SplitHostPort(endpoint[:idx] + ":22")
	}
	if err != nil {
		return nil, fmt.Errorf("unable to parse host from endpoint (%s): %q", endpoint, err)
	}
	root := filepath.Clean(endpoint[idx+1:])
	if runtime.GOOS == "windows" {
		root = strings.Replace(root, "\\", "/", -1)
	}
	// append suffix `/` removed by filepath.Clean()
	if strings.HasSuffix(endpoint[idx+1:], dirSuffix) {
		root = root + dirSuffix
	}

	if username == "" {
		u, _ := user.Current()
		if u != nil {
			username = u.Username
		}
	}
	username = unescape(username)
	var auth []ssh.AuthMethod
	if pass != "" {
		pass = unescape(pass)
		auth = append(auth, ssh.Password(pass))
	} else {
		auth = append(auth, ssh.KeyboardInteractive(SshInteractive))
	}

	var signers []ssh.Signer
	if privateKeyPath := os.Getenv("SSH_PRIVATE_KEY_PATH"); privateKeyPath != "" {
		key, err := ioutil.ReadFile(privateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read private key, error: %v", err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("unable to parse private key, error: %v", err)
		}
		signers = append(signers, signer)
	} else {
		home := filepath.Join(os.Getenv("HOME"), ".ssh")
		var algo = []string{"rsa", "dsa", "ecdsa", "ecdsa_sk", "ed25519", "xmss"}
		for _, a := range algo {
			key, err := ioutil.ReadFile(filepath.Join(home, "id_"+a))
			if err != nil {
				key, err = ioutil.ReadFile(filepath.Join(home, "id_"+a+"-cert"))
			}
			if err == nil {
				signer, err := ssh.ParsePrivateKey(key)
				if err == nil {
					signers = append(signers, signer)
				} else {
					logger.Debugf("load private key %s: %s", filepath.Join(home, "id_"+a), err)
				}
			}
		}
	}
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket != "" {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			logger.Errorf("Failed to open SSH_AUTH_SOCK: %v", err)
		} else {
			agent := agent.NewClient(conn)
			signer, err := agent.Signers()
			if err != nil {
				logger.Warnf("load signer from agent: %s", err)
			} else {
				signers = append(signers, signer...)
			}
		}
	}
	if len(signers) > 0 {
		auth = append(auth, ssh.PublicKeys(signers...))
	}

	config := &ssh.ClientConfig{
		User:            username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second * 3,
		Auth:            auth,
	}
	f := &sftpStore{
		host:   host,
		port:   port,
		root:   root,
		config: config,
	}

	c, err := f.getSftpConnection()
	if err != nil && strings.Contains(err.Error(), "unable to authenticate") &&
		pass == "" && os.Getenv("SSH_PRIVATE_KEY_PATH") == "" {
		fmt.Printf("%s@%s's password: ", username, host)
		var password []byte
		password, err = term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			return nil, fmt.Errorf("Read password: %s", err.Error())
		}
		f.config.Auth = append(f.config.Auth, ssh.Password(string(password)))
		c, err = f.getSftpConnection()
	}
	if err != nil {
		logger.Errorf("connect to %s failed: %s", host, err)
		return nil, err
	}
	defer f.putSftpConnection(&c, err)

	if strings.HasSuffix(root, dirSuffix) {
		logger.Debugf("Ensure directory %s", root)
		if err := c.sftpClient.MkdirAll(root); err != nil {
			return nil, fmt.Errorf("Creating directory %s failed: %q", root, err)
		}
	} else {
		dir := filepath.Dir(root)
		logger.Debugf("Ensure directory %s", dir)
		if err := c.sftpClient.MkdirAll(dir); err != nil {
			return nil, fmt.Errorf("Creating directory %s failed: %q", dir, err)
		}
	}

	return f, nil
}

func init() {
	Register("sftp", newSftp)
}
