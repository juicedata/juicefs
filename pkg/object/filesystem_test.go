/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package object

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

func testKeysEqual(objs []Object, expectedKeys []string) error {
	gottenKeys := make([]string, len(objs))
	for idx, obj := range objs {
		gottenKeys[idx] = obj.Key()
	}
	if len(gottenKeys) != len(expectedKeys) {
		return fmt.Errorf("Expected {%s}, got {%s}", strings.Join(expectedKeys, ", "),
			strings.Join(gottenKeys, ", "))
	}

	for idx, key := range gottenKeys {
		if key != expectedKeys[idx] {
			return fmt.Errorf("Expected {%s}, got {%s}", strings.Join(expectedKeys, ", "),
				strings.Join(gottenKeys, ", "))
		}
	}
	return nil
}

func TestFsFile(t *testing.T) {
	keys := []string{
		"x/",
		"x/x.txt",
		"xy.txt",
		"xyz/",
		"xyz/xyz.txt",
	}
	s0, _ := newDisk("/tmp/abc/unit-test/", "", "")
	// initialize directory tree
	for _, key := range keys {
		if err := s0.Put(key, bytes.NewReader([]byte{})); err != nil {
			t.Fatalf("PUT object `%s` failed: %q", key, err)
		}
	}
	// cleanup
	defer func() {
		// delete reversely, directory only can be deleted when it's empty
		idx := len(keys) - 1
		for ; idx >= 0; idx-- {
			if err := s0.Delete(keys[idx]); err != nil {
				t.Fatalf("DELETE object `%s` failed: %q", keys[idx], err)
			}
		}
	}()

	s, _ := newDisk("/tmp/abc/unit-test/x/", "", "")
	objs, err := listAll(s, "", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys := []string{"", "x.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}

	s, _ = newDisk("/tmp/abc/unit-test/x", "", "")
	objs, err = listAll(s, "", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys = []string{"/", "/x.txt", "y.txt", "yz/", "yz/xyz.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}

	s, _ = newDisk("/tmp/abc/unit-test/xy", "", "")
	objs, err = listAll(s, "", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys = []string{".txt", "z/", "z/xyz.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}
}

func TestFsSftp(t *testing.T) {
	// utils.SetLogLevel(logrus.DebugLevel)
	sftpHost := os.Getenv("SFTP_HOST")
	if sftpHost == "" {
		t.SkipNow()
	}
	sftpUser, sftpPass := os.Getenv("SFTP_USER"), os.Getenv("SFTP_PASS")
	s0, _ := newSftp(sftpHost, sftpUser, sftpPass)

	keys := []string{
		"x/",
		"x/x.txt",
		"xy.txt",
		"xyz/",
		"xyz/xyz.txt",
	}
	// initialize directory tree
	for _, key := range keys {
		if err := s0.Put(key, bytes.NewReader([]byte{})); err != nil {
			t.Fatalf("PUT object `%s` failed: %q", key, err)
		}
	}
	// cleanup
	defer func() {
		// delete reversely, directory only can be deleted when it's empty
		idx := len(keys) - 1
		for ; idx >= 0; idx-- {
			if err := s0.Delete(keys[idx]); err != nil {
				t.Fatalf("DELETE object `%s` failed: %q", keys[idx], err)
			}
		}
	}()

	s, _ := newSftp(sftpHost+"x/", sftpUser, sftpPass)
	objs, err := listAll(s, "", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys := []string{"", "x.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}

	s, _ = newSftp(sftpHost+"x", sftpUser, sftpPass)
	objs, err = listAll(s, "", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys = []string{"/", "/x.txt", "y.txt", "yz/", "yz/xyz.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}

	s, _ = newSftp(sftpHost+"xy", sftpUser, sftpPass)
	objs, err = listAll(s, "", "", 100)
	if err != nil {
		t.Fatalf("list failed: %s", err)
	}
	expectedKeys = []string{".txt", "z/", "z/xyz.txt"}
	if err = testKeysEqual(objs, expectedKeys); err != nil {
		t.Fatalf("testKeysEqual fail: %s", err)
	}
}
