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

package gateway

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	minio "github.com/minio/minio/cmd"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/vfs"
)

const (
	bucketLockDir = sep + metaBucket + sep + ".locks"

	bucketLockTimeout = 30 * time.Second
)

// must differ per process: a session id can be inherited by a restarted one
var bucketLockOwner = func() uint64 {
	id := uuid.New()
	return binary.BigEndian.Uint64(id[:8])
}()

type bucketLock struct {
	rw sync.RWMutex

	mu      sync.Mutex
	readers int
	inode   meta.Ino
}

type bucketLocks struct {
	mu    sync.Mutex
	locks map[string]*bucketLock
}

func (b *bucketLocks) get(bucket string) *bucketLock {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.locks == nil {
		b.locks = make(map[string]*bucketLock)
	}
	bl := b.locks[bucket]
	if bl == nil {
		bl = &bucketLock{}
		b.locks[bucket] = bl
	}
	return bl
}

// Lock files are never removed: a new inode would stop excluding current holders.
func (n *jfsObjects) lockInode(ctx context.Context, bl *bucketLock, bucket string) (meta.Ino, error) {
	if bl.inode != 0 {
		return bl.inode, nil
	}
	p := bucketLockDir + sep + bucket
	f, eno := n.fs.Open(mctx, p, vfs.MODE_MASK_W)
	if eno == syscall.ENOENT {
		if err := n.mkdirAll(ctx, bucketLockDir); err != nil {
			return 0, err
		}
		if f, eno = n.fs.Create(mctx, p, 0666, n.gConf.Umask); eno == syscall.EEXIST {
			f, eno = n.fs.Open(mctx, p, vfs.MODE_MASK_W)
		}
	}
	if eno != 0 {
		return 0, eno
	}
	defer f.Close(mctx)
	bl.inode = f.Inode()
	return bl.inode, nil
}

func (n *jfsObjects) flock(inode meta.Ino, ltype uint32) error {
	deadline := time.Now().Add(bucketLockTimeout)
	for {
		errno := n.fs.Meta().Flock(mctx, inode, bucketLockOwner, ltype, false)
		if errno == 0 {
			return nil
		}
		if !errors.Is(errno, syscall.EAGAIN) {
			return errno
		}
		if time.Now().After(deadline) {
			return minio.OperationTimedOut{}
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (n *jfsObjects) funlock(inode meta.Ino, bucket string) {
	if errno := n.fs.Meta().Flock(mctx, inode, bucketLockOwner, meta.F_UNLCK, false); errno != 0 {
		logger.Errorf("release lock of bucket %s: %s", bucket, errno)
	}
}

func (n *jfsObjects) rLockBucket(ctx context.Context, bucket string) (func(), error) {
	if !n.gConf.MultiBucket {
		return func() {}, nil
	}
	bl := n.bucketLocks.get(bucket)
	bl.rw.RLock()
	if err := n.acquireShared(ctx, bl, bucket); err != nil {
		bl.rw.RUnlock()
		return nil, err
	}
	return func() {
		n.releaseShared(bl, bucket)
		bl.rw.RUnlock()
	}, nil
}

func (n *jfsObjects) acquireShared(ctx context.Context, bl *bucketLock, bucket string) error {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	if bl.readers > 0 {
		bl.readers++
		return nil
	}
	ino, err := n.lockInode(ctx, bl, bucket)
	if err != nil {
		return err
	}
	if err := n.flock(ino, meta.F_RDLCK); err != nil {
		return err
	}
	bl.readers = 1
	return nil
}

func (n *jfsObjects) releaseShared(bl *bucketLock, bucket string) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	if bl.readers--; bl.readers > 0 {
		return
	}
	n.funlock(bl.inode, bucket)
}

func (n *jfsObjects) lockBucket(ctx context.Context, bucket string) (func(), error) {
	if !n.gConf.MultiBucket {
		return func() {}, nil
	}
	bl := n.bucketLocks.get(bucket)
	bl.rw.Lock()
	bl.mu.Lock()
	ino, err := n.lockInode(ctx, bl, bucket)
	bl.mu.Unlock()
	if err == nil {
		err = n.flock(ino, meta.F_WRLCK)
	}
	if err != nil {
		bl.rw.Unlock()
		return nil, err
	}
	return func() {
		n.funlock(ino, bucket)
		bl.rw.Unlock()
	}, nil
}
