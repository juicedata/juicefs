//go:build !nonfs
// +build !nonfs

/*
 * JuiceFS, Copyright 2023 Juicedata, Inc.
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
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/vmware/go-nfs-client/nfs"
	"github.com/vmware/go-nfs-client/nfs/rpc"
)

func TestNewNFSStoreMountError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("gomonkey cannot patch functions on darwin/arm64")
	}
	mountErr := errors.New("MNT3ERR_ACCES")
	patches := gomonkey.ApplyFunc(nfs.DialMount, func(string, time.Duration) (*nfs.Mount, error) {
		return &nfs.Mount{}, nil
	})
	defer patches.Reset()
	patches.ApplyMethodFunc(&nfs.Mount{}, "Mount", func(string, rpc.Auth) (*nfs.Target, error) {
		return nil, mountErr
	})

	store, err := newNFSStore("localhost:/export", "test", "", "")
	if store != nil {
		t.Fatalf("expected nil store, got %v", store)
	}
	if err == nil || err.Error() != "unable to mount localhost:/export: MNT3ERR_ACCES" {
		t.Fatalf("expected mount error, got %v", err)
	}
}
