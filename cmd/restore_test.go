package cmd

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRestore(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	paths := []string{"/jfs-dir", "/jfs-dir/a"}
	if err := os.Mkdir(fmt.Sprintf("%s%s", testMountPoint, "/jfs-dir"), 0777); err != nil {
		t.Fatalf("mkdirAll err: %s", err)
	}

	filename := fmt.Sprintf("%s%s", testMountPoint, "/jfs-dir/a")
	if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
		t.Fatalf("write file failed: %s", err)
	}

	for i := len(paths) - 1; i >= 0; i-- {
		path := paths[i]
		if err := os.Remove(fmt.Sprintf("%s%s", testMountPoint, path)); err != nil {
			t.Fatalf("removeAll err: %s", err)
		}
	}

	hour := time.Now().UTC().Format("2006-01-02-15")
	restoreArgs := []string{"", "restore", testMeta, hour}
	if err := Main(restoreArgs); err != nil {
		t.Fatalf("restore failed: %s", err)
	}

	hourDir := fmt.Sprintf("%s/%s/%s", testMountPoint, ".trash", hour)
	child, err := os.ReadDir(hourDir)
	if err != nil {
		t.Fatalf("read dir failed: %s", err)
	}
	for _, entry := range child {
		if strings.Contains(entry.Name(), "jfs-dir") {
			fileInfo, err := os.Stat(fmt.Sprintf("%s/%s/%s", hourDir, entry.Name(), "a"))
			if err != nil {
				t.Fatalf("stat failed: %s", err)
			}
			if fileInfo.IsDir() {
				t.Fatalf("restore failed, file: %v is dir", fileInfo)
			}
			return
		}
	}
	t.Fatalf("restore failed, cannot find file: %s in trash", "jfs-dir")
}

func TestRestorePutBack(t *testing.T) {
	mountTemp(t, nil, nil, nil)
	defer umountTemp(t)

	paths := []string{"/jfs-dir1", "/jfs-dir1/a"}
	if err := os.Mkdir(fmt.Sprintf("%s%s", testMountPoint, "/jfs-dir1"), 0777); err != nil {
		t.Fatalf("mkdirAll err: %s", err)
	}

	filename := fmt.Sprintf("%s%s", testMountPoint, "/jfs-dir1/a")
	if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
		t.Fatalf("write file failed: %s", err)
	}

	for i := len(paths) - 1; i >= 0; i-- {
		path := paths[i]
		if err := os.Remove(fmt.Sprintf("%s%s", testMountPoint, path)); err != nil {
			t.Fatalf("removeAll err: %s", err)
		}
	}

	hour := time.Now().UTC().Format("2006-01-02-15")
	restoreArgs := []string{"", "restore", testMeta, hour, "--put-back=true"}
	if err := Main(restoreArgs); err != nil {
		t.Fatalf("restore failed: %s", err)
	}

	fileInfo, err := os.Stat(fmt.Sprintf("%s%s", testMountPoint, "/jfs-dir1/a"))
	if err != nil {
		t.Fatalf("stat failed: %s", err)
	}
	if fileInfo.IsDir() {
		t.Fatalf("restore failed, file: %v is dir", fileInfo)
	}
}
