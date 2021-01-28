package sftp

import (
	"syscall"
	"testing"
)

func TestClientStatVFS(t *testing.T) {
	if *testServerImpl {
		t.Skipf("go server does not support FXP_EXTENDED")
	}
	sftp, cmd := testClient(t, READWRITE, NO_DELAY)
	defer cmd.Wait()
	defer sftp.Close()

	vfs, err := sftp.StatVFS("/")
	if err != nil {
		t.Fatal(err)
	}

	// get system stats
	s := syscall.Statfs_t{}
	err = syscall.Statfs("/", &s)
	if err != nil {
		t.Fatal(err)
	}

	// check some stats
	if vfs.Frsize != uint64(s.Frsize) {
		t.Fatalf("fr_size does not match, expected: %v, got: %v", s.Frsize, vfs.Frsize)
	}

	if vfs.Bsize != uint64(s.Bsize) {
		t.Fatalf("f_bsize does not match, expected: %v, got: %v", s.Bsize, vfs.Bsize)
	}

	if vfs.Namemax != uint64(s.Namelen) {
		t.Fatalf("f_namemax does not match, expected: %v, got: %v", s.Namelen, vfs.Namemax)
	}
}
