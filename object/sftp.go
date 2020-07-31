// Part of this file is borrowed from Rclone under MIT license:
// https://github.com/ncw/rclone/blob/master/backend/sftp/sftp.go

package object

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
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
	defaultObjectStorage
	host   string
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
	conn, err := net.Dial("tcp", f.host+":22")
	if err != nil {
		return nil, err
	}
	sshc, chans, reqs, err := ssh.NewClientConn(conn, f.host+":22", f.config)
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

func (f *sftpStore) path(key string) string {
	return filepath.Join(f.root, key)
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

	if off > 0 {
		if _, err := ff.Seek(off, 0); err != nil {
			ff.Close()
			return nil, err
		}
	}
	if limit > 0 {
		buf := make([]byte, limit)
		if n, err := ff.Read(buf); n == 0 && err != nil {
			return nil, err
		} else {
			return ioutil.NopCloser(bytes.NewBuffer(buf[:n])), nil
		}
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

	ff, err := c.sftpClient.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer c.sftpClient.Remove(tmp)
	_, err = io.Copy(ff, in)
	if err != nil {
		ff.Close()
		return err
	}
	err = ff.Close()
	if err != nil {
		return err
	}
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

func (f *sftpStore) Exists(key string) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)
	_, e := c.sftpClient.Stat(f.path(key))
	return e
}

func (f *sftpStore) Delete(key string) error {
	c, err := f.getSftpConnection()
	if err != nil {
		return err
	}
	defer f.putSftpConnection(&c, err)
	return c.sftpClient.Remove(f.path(key))
}

type sortFI []os.FileInfo

func (s sortFI) Len() int      { return len(s) }
func (s sortFI) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s sortFI) Less(i, j int) bool {
	name1 := s[i].Name()
	if s[i].IsDir() {
		name1 += "/"
	}
	name2 := s[j].Name()
	if s[j].IsDir() {
		name2 += "/"
	}
	return name1 < name2
}

func (f *sftpStore) doFind(c *sftp.Client, path, marker string, out chan *Object) {
	infos, err := c.ReadDir(path)
	if err != nil {
		logger.Errorf("readdir %s: %s", path, err)
		return
	}

	sort.Sort(sortFI(infos))
	for _, fi := range infos {
		p := path + "/" + fi.Name()
		key := p[len(f.root):]
		if key >= marker {
			if fi.IsDir() {
				out <- &Object{key + "/", 0, fi.ModTime(), true}
			} else {
				out <- &Object{key, fi.Size(), fi.ModTime(), false}
			}
		}
		if fi.IsDir() && (key >= marker || strings.HasPrefix(marker, key)) {
			f.doFind(c, p, marker, out)
		}
	}
}

func (f *sftpStore) find(c *sftp.Client, path, marker string, out chan *Object) {
	if path == "" {
		path = "."
	}

	if marker != "" {
		f.doFind(c, path, marker, out)
		return
	}

	// try the file with file path `path` as an object
	fi, err := c.Stat(path)
	if err != nil {
		logger.Errorf("Stat %s error: %s", path, err)
		return
	}
	if fi.IsDir() {
		out <- &Object{"", 0, fi.ModTime(), true}
		f.doFind(c, path, marker, out)
	} else {
		out <- &Object{"", fi.Size(), fi.ModTime(), false}
	}
}

func (f *sftpStore) List(prefix, marker string, limit int64) ([]*Object, error) {
	return nil, notSupported
}

func (f *sftpStore) ListAll(prefix, marker string) (<-chan *Object, error) {
	c, err := f.getSftpConnection()
	if err != nil {
		return nil, err
	}
	listed := make(chan *Object, 10240)
	go func() {
		defer f.putSftpConnection(&c, nil)
		f.find(c.sftpClient, filepath.Join(f.root, prefix), marker, listed)
		close(listed)
	}()
	return listed, nil
}

func newSftp(endpoint, user, pass string) ObjectStorage {
	parts := strings.Split(endpoint, ":")
	root := parts[1]

	config := &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Second * 3,
	}

	if pass != "" {
		config.Auth = append(config.Auth, ssh.Password(pass))
	}

	if privateKeyPath := os.Getenv("SSH_PRIVATE_KEY_PATH"); privateKeyPath != "" {
		key, err := ioutil.ReadFile(privateKeyPath)
		if err != nil {
			logger.Fatalf("unable to read private key, error: %v", err)
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			logger.Fatalf("unable to parse private key, error: %v", err)
		}

		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}

	return &sftpStore{
		host:   parts[0],
		root:   root,
		config: config,
	}
}

func init() {
	register("sftp", newSftp)
}
