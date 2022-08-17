//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package vfs

import (
	"fmt"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
)

func (v *VFS) setQuota(ctx meta.Context, ino Ino, capacity, inodes uint64) (err syscall.Errno) {
	//todo
	fmt.Printf("just for test ----ino: %d  capacity:%d, inodes: %d \n", ino, capacity, inodes)
	err = v.Meta.SetQuota(ctx, ino, capacity, inodes)
	return
}

func (v *VFS) fsckQuota(ctx meta.Context, ino Ino) (err syscall.Errno) {
	//todo
	fmt.Printf("just for test ----ino: %d \n", ino)
	err = v.Meta.FsckQuota(ctx, ino)
	return
}
