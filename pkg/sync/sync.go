/*
 * JuiceFS, Copyright 2018 Juicedata, Inc.
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

package sync

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net/http"
	"os"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juju/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vimeo/go-util/crc32combine"
)

// The max number of key per listing request
const (
	maxResults                   = 1000
	defaultPartSize              = 5 << 20
	bufferSize                   = 32 << 10
	maxBlock                     = defaultPartSize * 2
	multipartCheckpointThreshold = 4 << 30
	markDeleteSrc                = -1
	markDeleteDst                = -2
	markCopyPerms                = -3
	markChecksum                 = -4
)

var (
	handled                 *utils.Bar
	pending                 *utils.Bar
	copied, copiedBytes     *utils.Bar
	checked, checkedBytes   *utils.Bar
	skipped, skippedBytes   *utils.Bar
	excluded, excludedBytes *utils.Bar
	extra, extraBytes       *utils.Bar
	deleted, failed         *utils.Bar
	listedPrefix            *utils.Bar
	concurrent              = make(chan int, 10)
	limiter                 *mixedLimiter
	totalHandled            atomic.Int64
)

type mixedLimiter struct {
	global *globalLimit
	local  *ratelimit.Bucket
}

func (l *mixedLimiter) Wait(ctx context.Context, count int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.global != nil && l.global.healthy.Load() {
		if l.global.wait(ctx, count) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if l.local == nil {
		return nil
	}
	if d := l.local.Take(count); d > 0 {
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

type globalLimit struct {
	sync.Mutex
	balance int64
	due     time.Time
	need    int64
	waiters []*sync.Cond

	address   string
	localBW   int64
	healthy   atomic.Bool
	lastProbe time.Time
}
type req struct {
	// Positive numbers indicate a request, negative numbers indicate a payback.
	Bytes int64 `json:"bytes"`
}

type resp struct {
	Granted int64 `json:"granted"` // bytes
	Expired int64 `json:"expired"` // Millisecond
}

func (l *globalLimit) request(ctx context.Context, ask int64) (granted int64, expired int64, err error) {
	defer func() {
		if ctx.Err() != nil {
			return
		}
		ok := err == nil
		if prev := l.healthy.Swap(ok); prev && !ok {
			if l.localBW > 0 {
				logger.Warnf("traffic control %s is unavailable, switch to local bwlimit %s", l.address, utils.Mbps(l.localBW))
			} else {
				logger.Warnf("traffic control %s is unavailable, run without rate limit", l.address)
			}
		}
	}()
	r := req{Bytes: ask}
	data, err := json.Marshal(r)
	if err != nil {
		return 0, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, l.address, bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	result, err := http.DefaultClient.Do(httpReq)
	if err != nil || result.StatusCode != http.StatusOK {
		var status string
		if result != nil {
			status = http.StatusText(result.StatusCode)
			_ = result.Body.Close()
		}
		if ctx.Err() == nil {
			logger.Warnf("request traffic control %s failed: %s, http status: %s", l.address, err, status)
		}
		if err == nil {
			err = fmt.Errorf("http status: %s", status)
		}
		return 0, 0, err
	}
	defer result.Body.Close()
	content, err := io.ReadAll(result.Body)
	if err != nil {
		return 0, 0, err
	}
	res := resp{}
	if err = json.Unmarshal(content, &res); err != nil {
		return 0, 0, err
	}
	return res.Granted, res.Expired, nil
}

func (l *globalLimit) wait(ctx context.Context, bytes int64) bool {
	if ctx.Err() != nil {
		return false
	}
	l.Lock()
	defer l.Unlock()
	if bytes <= 0 || l.balance >= bytes && len(l.waiters) == 0 {
		l.balance -= bytes
		return true
	}
	if !l.healthy.Load() {
		return false
	}
	l.need += bytes

	var me = sync.NewCond(l)
	l.waiters = append(l.waiters, me)
	stop := context.AfterFunc(ctx, func() {
		l.Lock()
		me.Signal()
		l.Unlock()
	})
	defer stop()
	for l.waiters[0] != me && ctx.Err() == nil {
		me.Wait()
	}
	if ctx.Err() != nil {
		for i, w := range l.waiters {
			if w == me {
				l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
				if i == 0 && len(l.waiters) > 0 {
					l.waiters[0].Signal()
				}
				break
			}
		}
		l.need -= bytes
		return false
	}

	ok := l.balance >= bytes || l.requestMoreLocked(ctx, bytes)
	if ok {
		l.balance -= bytes
	}
	l.need -= bytes
	l.waiters = l.waiters[1:]
	if len(l.waiters) > 0 {
		l.waiters[0].Signal()
	}
	return ok
}

func (l *globalLimit) requestMoreLocked(ctx context.Context, bytes int64) bool {
	if !l.healthy.Load() || ctx.Err() != nil {
		return false
	}
	// request credit for other waiters together
	ask := l.need - l.balance
	if ask >= bytes*10 {
		// don't wait for too long
		ask = bytes * 10
	}
	l.Unlock()
	granted, expire, err := l.request(ctx, ask)
	l.Lock()
	if err != nil {
		return false
	}
	l.balance += granted
	l.due = time.Now().Add(time.Millisecond * time.Duration(expire))
	logger.Debugf("grant %d from %s until %s", granted, l.address, l.due)
	return true
}

func (l *globalLimit) checkBalance(ctx context.Context) {
	now := time.Now()
	if !l.healthy.Load() {
		if time.Since(l.lastProbe) >= time.Second {
			l.lastProbe = now
			if _, _, err := l.request(ctx, 0); err == nil {
				if l.localBW > 0 {
					logger.Infof("traffic control %s recovered, switch back to global limit from local bwlimit %s", l.address, utils.Mbps(l.localBW))
				} else {
					logger.Infof("traffic control %s recovered, switch back to global limit", l.address)
				}
			}
		}
		return
	}
	l.Lock()
	if l.balance > 0 && l.need == 0 && l.due.Before(now) {
		payback := l.balance
		if payback > 1<<30 {
			payback = 1 << 30
		}
		l.balance -= payback
		l.Unlock()
		_, _, _ = l.request(ctx, -payback)
	} else {
		l.Unlock()
	}
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)
var logger = utils.GetLogger("juicefs")

func cleanupCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}

func incrTotal(n int64) {
	totalHandled.Add(n)
}

func incrHandled(n int) {
	old := totalHandled.Swap(0)
	handled.IncrTotal(old)
	handled.IncrBy(n)
}

type chksumReader struct {
	io.Reader
	chksum uint32
	cal    bool
}

func (r *chksumReader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	if r.cal {
		r.chksum = crc32.Update(r.chksum, crcTable, p[:n])
	}
	return
}

type chksumWithSz struct {
	chksum uint32
	size   int64
}

// human readable bytes size
func formatSize(bytes int64) string {
	units := [7]string{" ", "K", "M", "G", "T", "P", "E"}
	if bytes < 1024 {
		return fmt.Sprintf("%v B", bytes)
	}
	z := 0
	v := float64(bytes)
	for v > 1024.0 {
		z++
		v /= 1024.0
	}
	return fmt.Sprintf("%.2f %siB", v, units[z])
}

func listAll(ctx context.Context, store object.ObjectStorage, prefix, start, end string, followLink, includeStart bool, sortDir string) (<-chan object.Object, error) {
	if sortDir != "" {
		return listAllWithExternalSort(ctx, store, prefix, start, end, followLink, includeStart, sortDir)
	}
	startTime := time.Now()
	logger.Debugf("Iterating objects from %s with prefix %s start %q", store, prefix, start)

	out := make(chan object.Object, maxResults*10)

	// As the result of object storage's List method doesn't include the marker key,
	// we try List the marker key separately.
	if includeStart && start != "" && strings.HasPrefix(start, prefix) {
		if obj, err := store.Head(ctx, start); err == nil {
			logger.Debugf("Found start key: %s from %s in %s", start, store, time.Since(startTime))
			out <- obj
		}
	}

	if ch, err := store.ListAll(ctx, prefix, start, followLink); err == nil {
		go func() {
			defer close(out)
			for obj := range ch {
				if obj != nil && end != "" && obj.Key() > end {
					break
				}
				select {
				case out <- obj:
				case <-ctx.Done():
					return
				}
			}
		}()
		return out, nil
	} else if !errors.Is(err, utils.ErrNotSUP) {
		return nil, err
	}

	marker := start
	logger.Debugf("Listing objects from %s marker %q", store, marker)

	objs, hasMore, nextToken, err := store.List(ctx, prefix, marker, "", "", maxResults, followLink)
	if errors.Is(err, utils.ErrNotSUP) {
		return object.ListAllWithDelimiter(ctx, store, prefix, start, end, followLink)
	}
	if err != nil {
		logger.Errorf("Can't list %s: %s", store, err.Error())
		return nil, err
	}
	logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
	go func() {
		defer close(out)
		lastkey := ""
		first := true
	END:
		for {
			for _, obj := range objs {
				key := obj.Key()
				if !first && key <= lastkey {
					logger.Errorf("The keys are out of order: marker %q, last %q current %q, try --external-sort-dir to sort them", marker, lastkey, key)
					select {
					case out <- nil:
					case <-ctx.Done():
					}
					break END
				}
				if end != "" && key > end {
					break END
				}
				lastkey = key
				// logger.Debugf("key: %s", key)
				select {
				case out <- obj:
				case <-ctx.Done():
					break END
				}
				first = false
			}
			if !hasMore || ctx.Err() != nil {
				break
			}

			marker = lastkey
			startTime = time.Now()
			logger.Debugf("Continue listing objects from %s marker %q", store, marker)
			var nextToken2 string
			objs, hasMore, nextToken2, err = store.List(ctx, prefix, marker, nextToken, "", maxResults, followLink)
			count := 0
			for err != nil && count < 3 && ctx.Err() == nil {
				logger.Warnf("Fail to list: %s, retry again", err.Error())
				// slow down
				timer := time.NewTimer(time.Millisecond * 100)
				select {
				case <-ctx.Done():
					timer.Stop()
					break END
				case <-timer.C:
				}

				objs, hasMore, nextToken2, err = store.List(ctx, prefix, marker, nextToken, "", maxResults, followLink)
				count++
			}
			logger.Debugf("Found %d object from %s in %s", len(objs), store, time.Since(startTime))
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				// Telling that the listing has failed
				select {
				case out <- nil:
				case <-ctx.Done():
				}
				logger.Errorf("Fail to list after %s: %s", marker, err.Error())
				break
			}
			nextToken = nextToken2
			if len(objs) > 0 && objs[0].Key() == marker {
				// workaround from a object store that is not compatible to S3.
				objs = objs[1:]
			}
		}
	}()
	return out, nil
}

// ListAll on all the keys that starts at marker from object storage.
func ListAll(ctx context.Context, store object.ObjectStorage, prefix, start, end string, followLink bool) (<-chan object.Object, error) {
	return listAll(ctx, store, prefix, start, end, followLink, true, "")
}

var bufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, bufferSize)
		return &buf
	},
}

func shouldRetry(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, utils.ErrSkipped) || errors.Is(err, utils.ErrExtlink) || errors.Is(err, utils.ErrUnsafeKey) {
		return false
	}

	var eno syscall.Errno
	if errors.As(err, &eno) {
		switch eno {
		case syscall.EAGAIN, syscall.EINTR, syscall.EBUSY, syscall.ETIMEDOUT, syscall.EIO:
			return true
		default:
			return false
		}
	}
	return true
}

func try(ctx context.Context, n int, f func() error) (err error) {
	for i := 0; i < n; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = f()
		if !shouldRetry(err) {
			return
		}
		logger.Debugf("Try %d failed: %s", i+1, err)
		if d := time.Second * time.Duration(i*i); d > 0 {
			timer := time.NewTimer(d)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return
}

func deleteObj(ctx context.Context, storage object.ObjectStorage, key string, dry bool) error {
	return deleteObjWithLimit(ctx, storage, key, dry, -1)
}

func deleteObjWithLimit(ctx context.Context, storage object.ObjectStorage, key string, dry bool, limit int64) error {
	if dry {
		logger.Debugf("Will delete %s from %s", key, storage)
		deleted.Increment()
		return nil
	}
	start := time.Now()
	if err := try(ctx, 3, func() error { return storage.Delete(ctx, key) }); err == nil {
		deleted.Increment()
		logger.Debugf("Deleted %s from %s in %s", key, storage, time.Since(start))
		return nil
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	} else if limit == 0 {
		logger.Warnf("Deferring deletion of non-empty directory %s from %s; the object limit may have been reached: %s", key, storage, err)
		return nil
	} else {
		failed.Increment()
		logger.Errorf("Failed to delete %s from %s in %s: %s", key, storage, time.Since(start), err)
		return err
	}
}

func needCopyPerms(o1, o2 object.Object) bool {
	f1 := o1.(object.File)
	f2 := o2.(object.File)
	return f2.Mode() != f1.Mode() || f2.Owner() != f1.Owner() || f2.Group() != f1.Group()
}

func copyPerms(dst object.ObjectStorage, obj object.Object, config *Config) {
	start := time.Now()
	key := obj.Key()
	fi := obj.(object.File)
	if !fi.IsSymlink() || !config.Links {
		// chmod needs to be executed after chown, because chown will change setuid setgid to be invalid.
		if err := dst.(object.FileSystem).Chown(key, fi.Owner(), fi.Group()); err != nil {
			logger.Warnf("Chown %s to (%s,%s): %s", key, fi.Owner(), fi.Group(), err)
		}
		if err := dst.(object.FileSystem).Chmod(key, fi.Mode()); err != nil {
			logger.Warnf("Chmod %s to %o: %s", key, fi.Mode(), err)
		}
	}
	logger.Debugf("Copied permissions (%s:%s:%s) for %s in %s", fi.Owner(), fi.Group(), fi.Mode(), key, time.Since(start))
}

func calPartChksum(ctx context.Context, objStor object.ObjectStorage, key string, offset, length int64) (uint32, error) {
	if limiter != nil {
		if err := limiter.Wait(ctx, length); err != nil {
			return 0, err
		}
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case concurrent <- 1:
		defer func() {
			<-concurrent
		}()
	}
	in, err := objStor.Get(ctx, key, offset, length)
	if err != nil {
		return 0, fmt.Errorf("dest get: %w", err)
	}
	defer in.Close()

	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	var chksum uint32
	for left := int(length); left > 0; left -= bufferSize {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		bs := bufferSize
		if left < bufferSize {
			bs = left
		}
		*buf = (*buf)[:bs]
		if _, err = io.ReadFull(in, *buf); err != nil {
			return 0, fmt.Errorf("dest read: %w", err)
		}
		chksum = crc32.Update(chksum, crcTable, *buf)
	}
	return chksum, nil
}

func calObjChksum(ctx context.Context, objStor object.ObjectStorage, key string, obj object.Object) (uint32, error) {
	var err error
	var chksum uint32
	if obj.Size() < maxBlock {
		return calPartChksum(ctx, objStor, key, 0, obj.Size())
	}
	partCtx, partCancel := context.WithCancel(ctx)
	defer partCancel()
	n := int((obj.Size()-1)/defaultPartSize) + 1
	errs := make(chan error, n)
	chksums := make([]chksumWithSz, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()
			sz := int64(defaultPartSize)
			if num == n-1 {
				sz = obj.Size() - int64(num)*defaultPartSize
			}
			chksum, err := calPartChksum(partCtx, objStor, key, int64(num)*defaultPartSize, sz)
			chksums[num] = chksumWithSz{chksum, sz}
			errs <- err
		}(i)
	}
	for i := 0; i < n; i++ {
		if err = <-errs; err != nil {
			partCancel()
			break
		}
	}
	wg.Wait()
	if err != nil {
		return 0, err
	}
	chksum = chksums[0].chksum
	for i := 1; i < n; i++ {
		chksum = crc32combine.CRC32Combine(crc32.Castagnoli, chksum, chksums[i].chksum, chksums[i].size)
	}
	return chksum, nil
}

func compObjPartBinary(ctx context.Context, src, dst object.ObjectStorage, key string, offset, length int64) error {
	if limiter != nil {
		if err := limiter.Wait(ctx, length); err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case concurrent <- 1:
		defer func() {
			<-concurrent
		}()
	}
	in, err := src.Get(ctx, key, offset, length)
	if err != nil {
		return fmt.Errorf("src get: %w", err)
	}
	defer in.Close()
	in2, err := dst.Get(ctx, key, offset, length)
	if err != nil {
		return fmt.Errorf("dest get: %w", err)
	}
	defer in2.Close()

	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)
	buf2 := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf2)
	for left := int(length); left > 0; left -= bufferSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		bs := bufferSize
		if left < bufferSize {
			bs = left
		}
		*buf = (*buf)[:bs]
		*buf2 = (*buf2)[:bs]
		if _, err = io.ReadFull(in, *buf); err != nil {
			return fmt.Errorf("src read: %w", err)
		}
		if _, err = io.ReadFull(in2, *buf2); err != nil {
			return fmt.Errorf("dest read: %w", err)
		}
		if !bytes.Equal(*buf, *buf2) {
			return fmt.Errorf("bytes not equal")
		}
	}
	return nil
}

func compObjBinary(ctx context.Context, src, dst object.ObjectStorage, key string, obj object.Object) (bool, error) {
	var err error
	if obj.Size() < maxBlock {
		err = compObjPartBinary(ctx, src, dst, key, 0, obj.Size())
	} else {
		partCtx, partCancel := context.WithCancel(ctx)
		defer partCancel()
		n := int((obj.Size()-1)/defaultPartSize) + 1
		errs := make(chan error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(num int) {
				defer wg.Done()
				sz := int64(defaultPartSize)
				if num == n-1 {
					sz = obj.Size() - int64(num)*defaultPartSize
				}
				errs <- compObjPartBinary(partCtx, src, dst, key, int64(num)*defaultPartSize, sz)
			}(i)
		}
		for i := 0; i < n; i++ {
			if err = <-errs; err != nil {
				partCancel()
				break
			}
		}
		wg.Wait()
	}
	equal := false
	if err != nil && err.Error() == "bytes not equal" {
		err = nil
	} else {
		equal = err == nil
	}
	return equal, err
}

func doCheckSum(ctx context.Context, src, dst object.ObjectStorage, key string, srcChksumPtr *uint32, obj object.Object, config *Config, equal *bool) error {
	if obj.IsSymlink() && config.Links && (config.CheckAll || config.CheckNew) {
		var srcLink, dstLink string
		var err error
		if s, ok := src.(object.SupportSymlink); ok {
			if srcLink, err = s.Readlink(key); err != nil {
				return err
			}
		}
		if s, ok := dst.(object.SupportSymlink); ok {
			if dstLink, err = s.Readlink(key); err != nil {
				return err
			}
		}
		*equal = srcLink == dstLink && srcLink != "" && dstLink != ""
		return nil
	}
	var err error
	if srcChksumPtr != nil {
		var srcChksum uint32
		var dstChksum uint32
		srcChksum = *srcChksumPtr
		dstChksum, err = calObjChksum(ctx, dst, key, obj)
		if err == nil {
			*equal = srcChksum == dstChksum
		} else {
			*equal = false
		}
	} else {
		*equal, err = compObjBinary(ctx, src, dst, key, obj)
	}
	return err
}

func checkSum(ctx context.Context, src, dst object.ObjectStorage, key string, srcChksum *uint32, obj object.Object, config *Config) (bool, error) {
	start := time.Now()
	var equal bool
	err := try(ctx, 3, func() error { return doCheckSum(ctx, src, dst, key, srcChksum, obj, config, &equal) })
	if err == nil {
		checked.Increment()
		checkedBytes.IncrInt64(obj.Size())
		if equal {
			logger.Debugf("Checked %s OK (and equal) in %s,", key, time.Since(start))
		} else {
			logger.Warnf("Checked %s OK (but NOT equal) in %s,", key, time.Since(start))
		}
	} else if ctx.Err() == nil {
		logger.Errorf("Failed to check %s in %s: %s", key, time.Since(start), err)
	}
	return equal, err
}

var fastStreamRead = map[string]struct{}{"file": {}, "hdfs": {}, "jfs": {}, "gluster": {}}
var streamWrite = map[string]struct{}{"file": {}, "hdfs": {}, "sftp": {}, "gs": {}, "wasb": {}, "ceph": {}, "swift": {}, "webdav": {}, "jfs": {}, "gluster": {}}
var readInMem = map[string]struct{}{"mem": {}, "etcd": {}, "redis": {}, "tikv": {}, "mysql": {}, "postgres": {}, "sqlite3": {}}

func inMap(obj object.ObjectStorage, m map[string]struct{}) bool {
	_, ok := m[strings.Split(obj.String(), "://")[0]]
	return ok
}

func doCopySingle(ctx context.Context, src, dst object.ObjectStorage, key string, size int64, calChksum bool) (uint32, error) {
	if size > maxBlock && !inMap(dst, readInMem) && !inMap(src, fastStreamRead) {
		var err error
		var in io.Reader
		downer := newParallelDownloader(ctx, src, key, size, downloadBufSize, concurrent)
		defer downer.Close()
		if inMap(dst, streamWrite) {
			in = downer
		} else {
			var f *os.File
			// download the object into disk
			if f, err = os.CreateTemp("", "rep"); err != nil {
				logger.Warnf("create temp file: %s", err)
				return doCopySingle0(ctx, src, dst, key, size, calChksum)
			}
			_ = os.Remove(f.Name()) // will be deleted after Close()
			defer f.Close()
			buf := bufPool.Get().(*[]byte)
			defer bufPool.Put(buf)
			// hide f.ReadFrom to avoid discarding buf
			if _, err = io.CopyBuffer(struct{ io.Writer }{f}, downer, *buf); err == nil {
				_, err = f.Seek(0, 0)
				in = f
			}
		}
		r := &chksumReader{in, 0, calChksum}
		if err == nil {
			err = dst.Put(ctx, key, r)
		}
		return r.chksum, err
	}
	return doCopySingle0(ctx, src, dst, key, size, calChksum)
}

func doCopySingle0(ctx context.Context, src, dst object.ObjectStorage, key string, size int64, calChksum bool) (uint32, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case concurrent <- 1:
		defer func() {
			<-concurrent
		}()
	}
	var in io.ReadCloser
	var err error
	if size == 0 {
		if key == "" && !object.IsFileSystem(dst) {
			ps := strings.SplitN(dst.String(), "/", 4)
			if len(ps) == 4 && ps[3] == "" {
				logger.Warnf("empty key is not support by %s, ignore it", dst)
				return 0, nil
			}
		}
		if object.IsFileSystem(src) {
			// for check permissions
			r, err := src.Get(ctx, key, 0, -1)
			if err != nil {
				return 0, err
			}
			_ = r.Close()
		}
		in = io.NopCloser(bytes.NewReader(nil))
	} else {
		in, err = src.Get(ctx, key, 0, size)
		if err != nil {
			if ctx.Err() == nil {
				if _, e := src.Head(ctx, key); os.IsNotExist(e) {
					logger.Debugf("Head src %s: %s", key, err)
					err = utils.ErrSkipped
				}
			}
			return 0, err
		}
	}
	r := &chksumReader{in, 0, calChksum}
	defer in.Close()
	err = dst.Put(ctx, key, newProgressReader(ctx, r, size))
	return r.chksum, err
}

type withProgress struct {
	ctx      context.Context
	r        io.Reader
	reserved int64 // bytes allowed but not yet consumed by a source read
}

func newProgressReader(ctx context.Context, r io.Reader, size int64) io.Reader {
	p := &withProgress{ctx: ctx, r: r}
	if size < 0 {
		return p
	}
	return io.LimitReader(p, size)
}

func (w *withProgress) Read(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if need := int64(len(b)) - w.reserved; need > 0 {
		if limiter != nil {
			if err := limiter.Wait(w.ctx, need); err != nil {
				return 0, err
			}
		}
		w.reserved += need
	}
	n, err := w.r.Read(b)
	w.reserved -= int64(n)
	if copiedBytes != nil {
		copiedBytes.IncrInt64(int64(n))
	}
	return n, err
}

func dynAlloc(size int) []byte {
	zeros := utils.PowerOf2(size)
	b := *dynPools[zeros].Get().(*[]byte)
	if cap(b) < size {
		panic(fmt.Sprintf("%d < %d", cap(b), size))
	}
	return b[:size]
}

func dynFree(b []byte) {
	dynPools[utils.PowerOf2(cap(b))].Put(&b)
}

var dynPools []*sync.Pool

func init() {
	dynPools = make([]*sync.Pool, 33) // 1 - 8G
	for i := 0; i < 33; i++ {
		func(bits int) {
			dynPools[i] = &sync.Pool{
				New: func() interface{} {
					b := make([]byte, 1<<bits)
					return &b
				},
			}
		}(i)
	}
}

func doUploadPart(ctx context.Context, src, dst object.ObjectStorage, srckey string, off, size int64, key, uploadID string, num int, calChksum bool) (*object.Part, uint32, error) {
	start := time.Now()
	sz := size
	var part *object.Part
	var chksum uint32
	err := try(ctx, 3, func() error {
		in, err := src.Get(ctx, srckey, off, sz)
		if err != nil {
			return err
		}
		defer in.Close()
		r := &chksumReader{in, 0, calChksum}
		pr := newProgressReader(ctx, r, size)
		err = utils.ErrNotSUP
		if obj, ok := dst.(object.SupportUploadPartStream); ok {
			part, err = obj.UploadPartStream(ctx, key, uploadID, num+1, pr)
		}

		if errors.Is(err, utils.ErrNotSUP) {
			data := dynAlloc(int(size))
			defer dynFree(data)
			if _, err = io.ReadFull(pr, data); err != nil {
				return err
			}
			// PartNumber starts from 1
			part, err = dst.UploadPart(ctx, key, uploadID, num+1, data)
		}
		chksum = r.chksum
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			logger.Warnf("Failed to copy data of %s part %d: %s", key, num, err)
		}
		return nil, 0, fmt.Errorf("part %d: %w", num, err)
	}
	logger.Debugf("Copied data of %s part %d in %s", key, num, time.Since(start))
	return part, chksum, nil
}

func choosePartSize(upload *object.MultipartUpload, size int64) int64 {
	partSize := int64(upload.MinPartSize)
	if partSize == 0 {
		partSize = defaultPartSize
	}
	if size > partSize*int64(upload.MaxCount) {
		partSize = size / int64(upload.MaxCount)
		partSize = ((partSize-1)>>20 + 1) << 20 // align to MB
	}
	return partSize
}

func doCopyRange(ctx context.Context, src, dst object.ObjectStorage, key string, off, size int64, upload *object.MultipartUpload, num int, calChksum bool) (*object.Part, uint32, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case concurrent <- 1:
		defer func() {
			<-concurrent
		}()
	}

	limits := dst.Limits()
	if size <= 32<<20 || !limits.IsSupportUploadPartCopy {
		return doUploadPart(ctx, src, dst, key, off, size, key, upload.UploadID, num, calChksum)
	}

	tmpkey := fmt.Sprintf("%s.part%d", key, num)
	var up *object.MultipartUpload
	var err error
	err = try(ctx, 3, func() error {
		up, err = dst.CreateMultipartUpload(ctx, tmpkey)
		return err
	})
	if err != nil {
		return nil, 0, fmt.Errorf("range(%d,%d): %w", off, size, err)
	}

	partSize := choosePartSize(up, size)
	n := int((size-1)/partSize) + 1
	logger.Debugf("Copying data of %s (range: %d,%d) as %d parts (size: %d): %s", key, off, size, n, partSize, up.UploadID)
	parts := make([]*object.Part, n)
	var tmpChksum uint32
	first := true

	for i := 0; i < n; i++ {
		sz := partSize
		if i == n-1 {
			sz = size - int64(i)*partSize
		}
		if err := ctx.Err(); err != nil {
			cCtx, cCancel := cleanupCtx(ctx)
			dst.AbortUpload(cCtx, tmpkey, up.UploadID)
			cCancel()
			return nil, 0, err
		}
		var chksum uint32
		parts[i], chksum, err = doUploadPart(ctx, src, dst, key, off+int64(i)*partSize, sz, tmpkey, up.UploadID, i, calChksum)
		if err != nil {
			cCtx, cCancel := cleanupCtx(ctx)
			dst.AbortUpload(cCtx, tmpkey, up.UploadID)
			cCancel()
			return nil, 0, fmt.Errorf("range(%d,%d): %w", off, size, err)
		}
		if calChksum {
			if first {
				tmpChksum = chksum
				first = false
			} else {
				tmpChksum = crc32combine.CRC32Combine(crc32.Castagnoli, tmpChksum, chksum, sz)
			}
		}
	}

	err = try(ctx, 3, func() error { return dst.CompleteUpload(ctx, tmpkey, up.UploadID, parts) })
	if err != nil {
		cCtx, cCancel := cleanupCtx(ctx)
		dst.AbortUpload(cCtx, tmpkey, up.UploadID)
		cCancel()
		return nil, 0, fmt.Errorf("multipart: %w", err)
	}
	var part *object.Part
	err = try(ctx, 3, func() error {
		part, err = dst.UploadPartCopy(ctx, key, upload.UploadID, num+1, tmpkey, 0, size)
		return err
	})
	cCtx, cCancel := cleanupCtx(ctx)
	_ = dst.Delete(cCtx, tmpkey)
	cCancel()
	return part, tmpChksum, err
}

func doCopyMultiple(ctx context.Context, src, dst object.ObjectStorage, key string, size int64, mtime time.Time, upload *object.MultipartUpload, calChksum bool, uploads multipartUploads) (uint32, error) {
	limits := dst.Limits()
	if size > limits.MaxPartSize*int64(upload.MaxCount) {
		return 0, fmt.Errorf("object size %d is too large to copy", size)
	}
	partSize := choosePartSize(upload, size)
	n := int((size-1)/partSize) + 1
	logger.Debugf("Copying data of %s as %d parts (size: %d): %s", key, n, partSize, upload.UploadID)
	partCtx, partCancel := context.WithCancel(ctx)
	defer partCancel()
	parts := make([]*object.Part, n)
	errs := make(chan error, n)
	chksums := make([]chksumWithSz, n)
	var err error
	var partWg sync.WaitGroup

	var state *multipartUploadState
	if uploads != nil {
		state = uploads.EnsureMultipartUploadState(key, size, mtime, partSize, upload)
	}

	for i := 0; i < n; i++ {
		sz := partSize
		if i == n-1 {
			sz = size - int64(i)*partSize
		}
		if state != nil {
			if p, chksum, ok := uploads.GetMultipartPart(state, i+1, calChksum); ok {
				parts[i] = p
				errs <- nil
				if calChksum {
					chksums[i] = chksumWithSz{chksum, sz}
				}
				continue
			}
		}
		partWg.Add(1)
		go func(num int) {
			defer partWg.Done()
			var copyErr error
			var chksum uint32
			parts[num], chksum, copyErr = doCopyRange(partCtx, src, dst, key, int64(num)*partSize, sz, upload, num, calChksum)
			chksums[num] = chksumWithSz{chksum, sz}
			if copyErr == nil {
				if state != nil {
					uploads.MarkMultipartPart(key, state, parts[num], chksum, calChksum)
				}
			}
			errs <- copyErr
		}(i)
	}

	for i := 0; i < n; i++ {
		if err = <-errs; err != nil {
			partCancel()
			break
		}
	}
	partWg.Wait()
	if err == nil {
		err = try(ctx, 3, func() error { return dst.CompleteUpload(ctx, key, upload.UploadID, parts) })
	}
	if err != nil {
		cCtx, cCancel := cleanupCtx(ctx)
		defer cCancel()
		if uploads == nil {
			dst.AbortUpload(cCtx, key, upload.UploadID)
		} else if ctx.Err() == nil {
			if _, e := src.Head(ctx, key); os.IsNotExist(e) {
				dst.AbortUpload(cCtx, key, upload.UploadID)
				uploads.FinishMultipartUpload(key)
			}
		}
		return 0, fmt.Errorf("multipart: %w", err)
	}
	if uploads != nil {
		uploads.FinishMultipartUpload(key)
	}
	var chksum uint32
	if calChksum {
		chksum = chksums[0].chksum
		for i := 1; i < n; i++ {
			chksum = crc32combine.CRC32Combine(crc32.Castagnoli, chksum, chksums[i].chksum, chksums[i].size)
		}
	}

	return chksum, nil
}

func CopyData(ctx context.Context, src, dst object.ObjectStorage, key string, size int64, calChksum bool) (uint32, error) {
	return copyData(ctx, src, dst, key, size, time.Time{}, calChksum, nil)
}

func copyData(ctx context.Context, src, dst object.ObjectStorage, key string, size int64, mtime time.Time, calChksum bool, uploads multipartUploads) (uint32, error) {
	start := time.Now()
	var err error
	var srcChksum uint32
	if size <= multipartCheckpointThreshold {
		uploads = nil
	}
	if size < max(int64(dst.Limits().MinPartSize*2), maxBlock) {
		err = try(ctx, 3, func() (err error) {
			srcChksum, err = doCopySingle(ctx, src, dst, key, size, calChksum)
			return
		})
	} else {
		var upload *object.MultipartUpload
		if uploads != nil {
			upload = uploads.FindMultipartUpload(key, size, mtime)
		}
		if upload != nil {
			srcChksum, err = doCopyMultiple(ctx, src, dst, key, size, mtime, upload, calChksum, uploads)
			if err != nil {
				if ctx.Err() != nil {
					return 0, err
				}
				cCtx, cCancel := cleanupCtx(ctx)
				dst.AbortUpload(cCtx, key, upload.UploadID)
				cCancel()
				uploads.FinishMultipartUpload(key)
				upload = nil
			}
		}
		if upload == nil {
			if upload, err = dst.CreateMultipartUpload(ctx, key); err == nil {
				srcChksum, err = doCopyMultiple(ctx, src, dst, key, size, mtime, upload, calChksum, uploads)
			} else if err == utils.ErrNotSUP {
				err = try(ctx, 3, func() (err error) {
					srcChksum, err = doCopySingle(ctx, src, dst, key, size, calChksum)
					return
				})
			} else if ctx.Err() == nil { // other error retry
				if err = try(ctx, 2, func() error {
					upload, err = dst.CreateMultipartUpload(ctx, key)
					return err
				}); err == nil {
					srcChksum, err = doCopyMultiple(ctx, src, dst, key, size, mtime, upload, calChksum, uploads)
				}
			}
		}
	}

	if err == nil {
		logger.Debugf("Copied data of %s (%d bytes) in %s", key, size, time.Since(start))
	} else if ctx.Err() == nil {
		if _, e := src.Head(ctx, key); os.IsNotExist(e) {
			logger.Debugf("Head src %s: %s", key, err)
			err = utils.ErrSkipped
			return 0, err
		}
		logger.Errorf("Failed to copy data of %s in %s: %s", key, time.Since(start), err)
	}
	return srcChksum, err
}

type holder struct {
	done chan struct{}
}

var muHolder sync.Mutex
var holders []*holder

func fetchTask(ctx context.Context, tasks chan object.Object) (t object.Object, done func()) {
	if ctx.Err() != nil {
		return nil, func() {}
	}
	muHolder.Lock()
	defer muHolder.Unlock()
	if len(holders) > 0 {
		h := holders[len(holders)-1]
		holders = holders[:len(holders)-1]
		muHolder.Unlock()
		select {
		case <-h.done:
		case <-ctx.Done():
			muHolder.Lock()
			return nil, func() {}
		}
		muHolder.Lock()
	}
	select {
	case t = <-tasks:
		if t == nil {
			return nil, func() {}
		}
	case <-ctx.Done():
		return nil, func() {}
	}
	size := t.Size()
	if size == markChecksum {
		size = withoutSize(t).Size()
	}
	if size >= maxBlock*2 {
		done := make(chan struct{})
		h := &holder{done: done}
		n := min(int(size)/maxBlock, 20)
		for i := 1; i < n; i++ {
			holders = append(holders, h)
		}
		return t, func() { close(done) }
	} else {
		return t, func() {}
	}
}

func worker(ctx context.Context, tasks chan object.Object, src, dst object.ObjectStorage, config *Config, checkpointMgr *CheckpointManager, uploads multipartUploads) {
	for {
		obj, done := fetchTask(ctx, tasks)
		if obj == nil {
			break
		}
		key := obj.Key()
		var taskErr error

		switch obj.Size() {
		case markDeleteSrc:
			taskErr = deleteObj(ctx, src, key, config.Dry)
		case markDeleteDst:
			taskErr = deleteObj(ctx, dst, key, config.Dry)
		case markCopyPerms:
			if config.Dry {
				logger.Debugf("Will copy permissions for %s", key)
			} else {
				copyPerms(dst, withoutSize(obj), config)
			}
			copied.Increment()
		case markChecksum:
			if config.Dry {
				logger.Debugf("Will compare checksum for %s", key)
				checked.Increment()
				break
			}
			obj = withoutSize(obj)
			if equal, err := checkSum(ctx, src, dst, key, nil, obj, config); err != nil {
				if ctx.Err() == nil {
					failed.Increment()
				}
				taskErr = err
				break
			} else if equal {
				if config.DeleteSrc {
					if obj.IsDir() {
						srcDelayDelMu.Lock()
						srcDelayDel = append(srcDelayDel, key)
						srcDelayDelMu.Unlock()
					} else {
						taskErr = deleteObj(ctx, src, key, false)
					}
				} else if config.Perms && (!obj.IsSymlink() || !config.Links) {
					if o, e := dst.Head(ctx, key); e == nil {
						if needCopyPerms(obj, o) {
							copyPerms(dst, obj, config)
							copied.Increment()
						} else {
							skipped.Increment()
							skippedBytes.IncrInt64(obj.Size())
						}
					} else {
						if ctx.Err() == nil {
							logger.Warnf("Failed to head object %s: %s", key, e)
							failed.Increment()
						}
						taskErr = e
					}
				} else {
					skipped.Increment()
					skippedBytes.IncrInt64(obj.Size())
				}
				break
			}
			// checkSum not equal, copy the object
			fallthrough
		default:
			if config.Dry {
				logger.Debugf("Will copy %s (%d bytes)", obj.Key(), obj.Size())
				copied.Increment()
				copiedBytes.IncrInt64(obj.Size())
				break
			}
			var err error
			var srcChksum uint32

			if config.Links && obj.IsSymlink() {
				if err = copyLink(ctx, src, dst, key); err != nil && ctx.Err() == nil {
					logger.Errorf("copy link %s failed: %s", key, err)
				}
			} else {
				srcChksum, err = copyData(ctx, src, dst, key, obj.Size(), obj.Mtime(), config.CheckAll || config.CheckNew, uploads)
			}
			if errors.Is(err, utils.ErrExtlink) {
				logger.Warnf("Skip external link %s: %s", key, err)
				err = utils.ErrSkipped
			}

			if err == nil && config.CheckChange {
				err = checkChange(ctx, src, dst, obj, key, config)
			}

			if err == nil && (config.CheckAll || config.CheckNew) {
				var equal bool
				if equal, err = checkSum(ctx, src, dst, key, &srcChksum, obj, config); err == nil && !equal {
					err = fmt.Errorf("checksums of copied object %s don't match", key)
				}
			}
			if err == nil {
				if mc, ok := dst.(object.MtimeChanger); ok {
					if err = mc.Chtimes(obj.Key(), obj.Mtime()); err != nil && !errors.Is(err, utils.ErrNotSUP) {
						logger.Warnf("Update mtime of %s: %s", key, err)
					}
				}
				if config.Perms {
					copyPerms(dst, obj, config)
				}
				copied.Increment()
			} else if errors.Is(err, utils.ErrSkipped) {
				skipped.Increment()
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				taskErr = err
			} else {
				failed.Increment()
				logger.Errorf("Failed to copy object %s: %s", key, err)
				taskErr = err
			}
			if taskErr == nil && config.DeleteSrcAfter {
				if obj.IsDir() {
					srcDelayDelMu.Lock()
					srcDelayDel = append(srcDelayDel, key)
					srcDelayDelMu.Unlock()
				} else {
					taskErr = deleteObj(ctx, src, key, false)
				}
			}
		}

		trackCheckpointCompletion(key, taskErr, checkpointMgr, config)
		if !errors.Is(taskErr, context.Canceled) && !errors.Is(taskErr, context.DeadlineExceeded) {
			incrHandled(1)
		}
		done()
	}
}

func checkChange(ctx context.Context, src, dst object.ObjectStorage, obj object.Object, key string, config *Config) error {
	if obj == nil || config.Links && obj.IsSymlink() {
		return nil // ignore symlink
	}
	if cur, err := src.Head(ctx, key); err == nil {
		if !config.CheckAll && !config.CheckNew {
			checked.Increment()
			checkedBytes.IncrInt64(obj.Size())
		}
		equal := cur.Size() == obj.Size()
		if equal && !cur.Mtime().Equal(obj.Mtime()) {
			// Head of an object may not return the millisecond part of mtime as List
			equal = cur.Mtime().Unix() == obj.Mtime().Unix() && cur.Mtime().UnixMilli()%1000 == 0
		}
		if !equal {
			return fmt.Errorf("%s changed during sync. Original: size=%d, mtime=%s; Current: size=%d, mtime=%s",
				cur.Key(), obj.Size(), obj.Mtime(), cur.Size(), cur.Mtime())
		}
		if dstObj, err := dst.Head(ctx, key); err == nil {
			if cur.Size() != dstObj.Size() {
				return fmt.Errorf("copied %s size mismatch: original=%d, current=%d", key, obj.Size(), dstObj.Size())
			}
			return nil
		} else {
			return fmt.Errorf("check %s in %s: %w", key, dst, err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("object %s was removed during sync", key)
	} else {
		return fmt.Errorf("check %s in %s: %w", key, src, err)
	}
}

func copyLink(ctx context.Context, src object.ObjectStorage, dst object.ObjectStorage, key string) error {
	var p string
	var err error
	if p, err = src.(object.SupportSymlink).Readlink(key); err != nil {
		return err
	}
	return try(ctx, 3, func() (err error) {
		// TODO: use relative path based on option
		if err := dst.(object.SupportSymlink).Symlink(p, key); err != nil {
			if info, err := dst.Head(ctx, key); err == nil && info.IsSymlink() {
				if cPath, err2 := dst.(object.SupportSymlink).Readlink(key); err2 == nil && p == cPath {
					return nil
				}
			}
			if err := dst.Delete(ctx, key); err != nil {
				logger.Errorf("delete %s from %s failed: %s", key, dst, err)
				return err
			}
			if err := dst.(object.SupportSymlink).Symlink(p, key); err != nil {
				logger.Warnf("symlink %s to %s error %s", p, key, err)
				return err
			}
		}
		return nil
	})
}

type objWithSize struct {
	object.Object
	nsize int64
}

func (o *objWithSize) Size() int64 {
	return o.nsize
}

type fileWithSize struct {
	object.File
	nsize int64
}

func (o *fileWithSize) Size() int64 {
	return o.nsize
}

type objWithMultipart struct {
	object.Object
	checkpoint *multipartUploadState
}

type fileWithMultipart struct {
	object.File
	checkpoint *multipartUploadState
}

func withSize(o object.Object, nsize int64) object.Object {
	if f, ok := o.(object.File); ok {
		return &fileWithSize{f, nsize}
	}
	return &objWithSize{o, nsize}
}

func withMultipart(o object.Object, checkpoint *multipartUploadState) object.Object {
	if f, ok := o.(object.File); ok {
		return &fileWithMultipart{f, checkpoint}
	}
	return &objWithMultipart{o, checkpoint}
}

func multipartCheckpoint(o object.Object) *multipartUploadState {
	switch w := o.(type) {
	case *objWithMultipart:
		return w.checkpoint
	case *fileWithMultipart:
		return w.checkpoint
	}
	return nil
}

func withoutMultipart(o object.Object) object.Object {
	switch w := o.(type) {
	case *objWithMultipart:
		return w.Object
	case *fileWithMultipart:
		return w.File
	}
	return o
}

func withoutSize(o object.Object) object.Object {
	switch w := o.(type) {
	case *objWithSize:
		return w.Object
	case *fileWithSize:
		return w.File
	}
	return o
}

var dstDelayDelMu sync.Mutex
var dstDelayDel []string
var srcDelayDelMu sync.Mutex
var srcDelayDel []string

func handleExtraObject(ctx context.Context, tasks chan<- object.Object, dstobj object.Object, config *Config, checkpointMgr *CheckpointManager, prefix string) bool {
	if checkpointMgr.isCheckpointKey(dstobj.Key()) {
		return false
	}
	if config.Limit == 0 {
		return true
	}
	if !config.DeleteDst || !config.Dirs && dstobj.IsDir() {
		logger.Debug("Ignore extra object", dstobj.Key())
		extra.Increment()
		extraBytes.IncrInt64(dstobj.Size())
		return false
	}
	incrTotal(1)
	if dstobj.IsDir() {
		dstDelayDelMu.Lock()
		dstDelayDel = append(dstDelayDel, dstobj.Key())
		dstDelayDelMu.Unlock()
	} else {
		obj := withSize(dstobj, markDeleteDst)
		if checkpointMgr != nil {
			checkpointMgr.AddPendingKey(prefix, obj)
		}
		select {
		case tasks <- obj:
		case <-ctx.Done():
			return true
		}
	}
	if config.Limit > 0 {
		config.Limit--
	}
	return config.Limit == 0
}

func startSingleProducer(ctx context.Context, tasks chan<- object.Object, src, dst object.ObjectStorage, prefix string, config *Config, checkpointMgr *CheckpointManager) error {
	start, end := config.Start, config.End
	logger.Debugf("maxResults: %d, defaultPartSize: %d, maxBlock: %d", maxResults, defaultPartSize, maxBlock)

	startAfter := start
	includeStart := true
	if lastKey := checkpointMgr.GetLastListedKey(prefix); lastKey != "" {
		startAfter = lastKey
		includeStart = false
	}

	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	srckeys, err := listAll(listCtx, src, prefix, startAfter, end, !config.Links, includeStart, config.ExternalSortDir)
	if err != nil {
		return fmt.Errorf("list %s: %w", src, err)
	}

	var dstkeys <-chan object.Object
	if config.ForceUpdate && !config.DeleteDst {
		t := make(chan object.Object)
		close(t)
		dstkeys = t
	} else {
		dstkeys, err = listAll(listCtx, dst, prefix, startAfter, end, !config.Links, includeStart, config.ExternalSortDir)
		if err != nil {
			return fmt.Errorf("list %s: %w", dst, err)
		}
	}
	return produce(ctx, tasks, srckeys, dstkeys, config, checkpointMgr, prefix)
}

func produce(ctx context.Context, tasks chan<- object.Object, srckeys, dstkeys <-chan object.Object, config *Config, checkpointMgr *CheckpointManager, prefix string) (retErr error) {
	srckeys = filter(ctx, srckeys, config.rules, config)
	dstkeys = filter(ctx, dstkeys, config.rules, config)
	var dstobj object.Object
	var (
		skip, skipBytes int64
		lastUpdate      time.Time
	)
	flushProgress := func() {
		skipped.IncrInt64(skip)
		skippedBytes.IncrInt64(skipBytes)
		incrHandled(int(skip))
		skip, skipBytes = 0, 0
	}
	defer flushProgress()

	defer func() {
		if checkpointMgr != nil && retErr == nil && ctx.Err() == nil {
			checkpointMgr.MarkListDone(prefix)
		}
	}()

	skipIt := func(obj object.Object) {
		if checkpointMgr != nil {
			checkpointMgr.UpdateLastListedKey(prefix, obj)
		}
		skip++
		skipBytes += obj.Size()
		if skip > 100 || time.Since(lastUpdate) > time.Millisecond*100 {
			lastUpdate = time.Now()
			flushProgress()
		}
	}

	sendTask := func(obj object.Object) bool {
		if checkpointMgr != nil {
			checkpointMgr.AddPendingKey(prefix, obj)
		}
		select {
		case tasks <- obj:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for obj := range srckeys {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if obj == nil {
			return fmt.Errorf("listing failed, stop syncing, waiting for pending ones")
		}

		if !config.Dirs && obj.IsDir() && (!config.Links || !obj.IsSymlink()) {
			if checkpointMgr != nil {
				checkpointMgr.UpdateLastListedKey(prefix, obj)
			}
			logger.Debug("Ignore directory ", obj.Key())
			continue
		}

		if dstobj != nil && obj.Key() > dstobj.Key() {
			if handleExtraObject(ctx, tasks, dstobj, config, checkpointMgr, prefix) {
				return ctx.Err()
			}
			dstobj = nil
		}
		if dstobj == nil {
			for dstobj = range dstkeys {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if dstobj == nil {
					return fmt.Errorf("listing failed, stop syncing, waiting for pending ones")
				}
				if obj.Key() <= dstobj.Key() {
					break
				}
				if handleExtraObject(ctx, tasks, dstobj, config, checkpointMgr, prefix) {
					return ctx.Err()
				}
				dstobj = nil
			}
		}

		if config.Limit >= 0 {
			if config.Limit == 0 {
				return nil
			}
			config.Limit--
		}
		incrTotal(1)
		// FIXME: there is a race when source is modified during coping
		if dstobj == nil || obj.Key() < dstobj.Key() {
			if config.Existing {
				skipIt(obj)
				continue
			}
			if !sendTask(obj) {
				return ctx.Err()
			}
		} else { // obj.key == dstobj.key
			if config.IgnoreExisting {
				skipIt(obj)
				dstobj = nil
				continue
			}
			if config.ForceUpdate ||
				(config.Update && obj.Mtime().Unix() > dstobj.Mtime().Unix()) ||
				(!config.Update && obj.Size() != dstobj.Size()) {
				if !sendTask(obj) {
					return ctx.Err()
				}
			} else if config.Update && obj.Mtime().Unix() < dstobj.Mtime().Unix() {
				skipIt(obj)
			} else if config.CheckAll { // two objects are likely the same
				if !sendTask(withSize(obj, markChecksum)) {
					return ctx.Err()
				}
			} else if config.DeleteSrc {
				if obj.IsDir() {
					srcDelayDelMu.Lock()
					srcDelayDel = append(srcDelayDel, obj.Key())
					srcDelayDelMu.Unlock()
					if checkpointMgr != nil {
						checkpointMgr.UpdateLastListedKey(prefix, obj)
					}
				} else {
					if !sendTask(withSize(obj, markDeleteSrc)) {
						return ctx.Err()
					}
				}
			} else if config.Perms && needCopyPerms(obj, dstobj) {
				if !sendTask(withSize(obj, markCopyPerms)) {
					return ctx.Err()
				}
			} else {
				skipIt(obj)
			}
			dstobj = nil
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if config.DeleteDst {
		if dstobj != nil {
			if handleExtraObject(ctx, tasks, dstobj, config, checkpointMgr, prefix) {
				return ctx.Err()
			}
		}
		for dstobj = range dstkeys {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if dstobj == nil {
				return fmt.Errorf("listing failed, stop syncing, waiting for pending ones")
			}
			if handleExtraObject(ctx, tasks, dstobj, config, checkpointMgr, prefix) {
				return ctx.Err()
			}
		}
	}
	return ctx.Err()
}

type rule struct {
	pattern string
	include bool
}

func parseRule(name, p string) rule {
	if runtime.GOOS == "windows" {
		p = strings.ReplaceAll(p, "\\", "/")
	}
	return rule{pattern: p, include: name == "-include"}
}

func parseIncludeRules(args []string) (rules []rule) {
	l := len(args)
	for i, a := range args {
		if strings.HasPrefix(a, "--") {
			a = a[1:]
		}
		if l-1 > i && (a == "-include" || a == "-exclude") {
			if _, err := path.Match(args[i+1], "xxxx"); err != nil {
				logger.Warnf("ignore invalid pattern: %q %q", a, args[i+1])
				continue
			}
			rules = append(rules, parseRule(a, args[i+1]))
		} else if strings.HasPrefix(a, "-include=") || strings.HasPrefix(a, "-exclude=") {
			if s := strings.Split(a, "="); len(s) == 2 && s[1] != "" {
				if _, err := path.Match(s[1], "xxxx"); err != nil {
					logger.Warnf("ignore invalid pattern: %q", a)
					continue
				}
				rules = append(rules, parseRule(s[0], s[1]))
			}
		}
	}
	return
}

func filterKey(o object.Object, now time.Time, rules []rule, config *Config) bool {
	var ok bool = true
	if !o.IsDir() && !o.IsSymlink() {
		ok = o.Size() >= int64(config.MinSize) && o.Size() <= int64(config.MaxSize)
		if ok && config.MaxAge > 0 {
			ok = o.Mtime().After(now.Add(-config.MaxAge))
		}
		if ok && config.MinAge > 0 {
			ok = o.Mtime().Before(now.Add(-config.MinAge))
		}
		if ok && !config.StartTime.IsZero() {
			ok = o.Mtime().After(config.StartTime)
		}
		if ok && !config.EndTime.IsZero() {
			ok = o.Mtime().Before(config.EndTime)
		}
	}
	if ok {
		if config.MatchFullPath {
			ok = matchFullPath(rules, o.Key())
		} else {
			ok = matchLeveledPath(rules, o.Key())
		}
	}
	return ok
}

func filter(ctx context.Context, keys <-chan object.Object, rules []rule, config *Config) <-chan object.Object {
	r := make(chan object.Object)
	now := time.Now()
	go func() {
		defer close(r)
		for o := range keys {
			if o == nil {
				// Telling that the listing has failed
				select {
				case r <- nil:
				case <-ctx.Done():
				}
				break
			}
			if filterKey(o, now, rules, config) {
				select {
				case r <- o:
				case <-ctx.Done():
					return
				}
			} else {
				logger.Debugf("exclude %s size: %d, mtime: %s", o.Key(), o.Size(), o.Mtime())
				excluded.Increment()
				excludedBytes.IncrInt64(o.Size())
			}
		}
	}()
	return r
}

func matchTwoStar(p string, s []string) bool {
	if len(s) == 0 {
		return p == "*"
	}
	idx := strings.Index(p, "**")
	if idx == -1 {
		ok, _ := path.Match(p, strings.Join(s, "/"))
		return ok
	}
	ok, _ := path.Match(p[:idx+1], s[0])
	if !ok {
		return false
	}
	for i := 0; i <= len(s); i++ {
		tp := p[idx+1:]
		if i == 0 {
			tp = p[:idx] + p[idx+1:]
		}
		if matchTwoStar(tp, s[i:]) {
			return true
		}
	}
	return false
}

func matchPrefix(p, s []string) bool {
	if len(p) == 0 || len(s) == 0 {
		return len(p) == len(s)
	}
	first := p[0]
	n := len(s)
	switch {
	case first == "***":
		return true
	case strings.Contains(first, "**"):
		for i := 1; i <= n; i++ {
			if matchTwoStar(first, s[:i]) && matchPrefix(p[1:], s[i:]) {
				return true
			}
		}
		return false
	default:
		ok, _ := path.Match(first, s[0])
		return ok && matchPrefix(p[1:], s[1:])
	}
}

func matchSuffix(p, s []string) bool {
	if len(p) == 0 {
		return true
	}
	last := p[len(p)-1]
	if len(s) == 0 {
		return len(p) == 1 && (last == "***" || last == "**")
	}
	prefix := p[:len(p)-1]
	n := len(s)
	switch {
	case last == "***":
		for i := 0; i < n; i++ {
			if matchSuffix(prefix, s[:i]) {
				return true
			}
		}
		return false
	case strings.Contains(last, "**"):
		for i := 0; i < n; i++ {
			if matchTwoStar(last, s[i:]) && matchSuffix(prefix, s[:i]) {
				return true
			}
		}
		return false
	default:
		ok, _ := path.Match(last, s[n-1])
		return ok && matchSuffix(prefix, s[:n-1])
	}
}

func matchFullPath(rules []rule, key string) bool {
	ps := strings.Split(key, "/")
	for _, rule := range rules {
		p := strings.Split(rule.pattern, "/")
		var ok bool
		if p[0] == "" {
			if ps[0] != "" {
				p = p[1:]
			}
			ok = matchPrefix(p, ps)
		} else {
			ok = matchSuffix(p, ps)
		}
		if ok {
			if rule.include {
				break // try next level
			} else {
				return false
			}
		}
	}
	return true
}

// Consistent with rsync behavior, the matching order is adjusted according to the order of the "include" and "exclude" options
func matchLeveledPath(rules []rule, key string) bool {
	parts := strings.Split(key, "/")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		for _, rule := range rules {
			ps := parts[:i+1]
			p := strings.Split(rule.pattern, "/")
			if i < len(parts)-1 && (p[len(p)-1] == "" || p[len(p)-1] == "***") {
				ps = append(append([]string{}, ps...), "") // don't overwrite parts
			}
			var ok bool
			if p[0] == "" {
				if ps[0] != "" {
					p = p[1:]
				}
				ok = matchPrefix(p, ps)
			} else {
				ok = matchSuffix(p, ps)
			}
			if ok {
				if rule.include {
					break // try next level
				} else {
					return false
				}
			}
		}
	}
	return true
}

// delimiterListLimit returns the page size to list a level of store with delimiter.
func delimiterListLimit(store object.ObjectStorage) int64 {
	if strings.HasPrefix(store.String(), "file://") || strings.HasPrefix(store.String(), "nfs://") ||
		strings.HasPrefix(store.String(), "gluster://") || strings.HasPrefix(store.String(), "jfs://") ||
		strings.HasPrefix(store.String(), "hdfs://") || strings.HasPrefix(store.String(), "webdav://") {
		return math.MaxInt64
	}
	return maxResults
}

func listCommonPrefix(ctx context.Context, store object.ObjectStorage, prefix string, cp chan object.Object, followLink bool, startAfter string, onChildPrefix func(string), sortDir string) (<-chan object.Object, error) {
	if sortDir != "" {
		return listCommonPrefixWithExternalSort(ctx, store, prefix, cp, followLink, startAfter, onChildPrefix, sortDir)
	}
	var total []object.Object
	var objs []object.Object
	var err error
	var nextToken string
	marker := startAfter
	var hasMore bool
	thisListMaxResults := delimiterListLimit(store)
	for {
		objs, hasMore, nextToken, err = store.List(ctx, prefix, marker, nextToken, "/", thisListMaxResults, followLink)
		if err != nil {
			return nil, err
		}
		if len(objs) > 0 {
			total = append(total, objs...)
			marker = objs[len(objs)-1].Key()
		}
		if !hasMore {
			break
		}
	}
	srckeys := make(chan object.Object, 1000)
	go func() {
		defer close(srckeys)
		for _, o := range total {
			if o.IsDir() && o.Key() > prefix {
				if cp != nil {
					if onChildPrefix != nil {
						onChildPrefix(o.Key())
					}
					select {
					case cp <- o:
					case <-ctx.Done():
						return
					}
				}
			} else {
				select {
				case srckeys <- o:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return srckeys, nil
}

// restorePrefixFromCheckpoint restores the pending keys of a prefix from checkpoint and restarts the producer if the listing of the prefix is not done.
// It returns true if the prefix is restored from checkpoint, otherwise false.
func restorePrefixFromCheckpoint(ctx context.Context, tasks chan<- object.Object, src, dst object.ObjectStorage, prefix string, config *Config, checkpointMgr *CheckpointManager) bool {
	objs, listDone, listDepth, found := checkpointMgr.RestorePrefix(prefix)
	if !found {
		return false
	}
	if objs == nil {
		// Already restored by another goroutine (dedup)
		return true
	}

	logger.Debugf("Restore prefix %q from checkpoint, restoreObjs: %d, listDone: %v", prefix, len(objs), listDone)
	for _, obj := range objs {
		incrTotal(1)
		select {
		case tasks <- obj:
		case <-ctx.Done():
			return true
		}
	}

	if !listDone && ctx.Err() == nil {
		if err := startProducer(ctx, tasks, src, dst, prefix, listDepth, config, checkpointMgr); err != nil && ctx.Err() == nil {
			logger.Errorf("Failed to restart producer for prefix %s: %v", prefix, err)
			failed.Increment()
		}
	}
	return true
}

func produceFromList(ctx context.Context, tasks chan<- object.Object, src, dst object.ObjectStorage, config *Config, checkpointMgr *CheckpointManager) error {
	f, err := os.Open(config.FilesFrom)
	if err != nil {
		return fmt.Errorf("open %s: %s", config.FilesFrom, err)
	}
	defer f.Close()

	prefixs := make(chan string, config.Threads)
	var wg sync.WaitGroup
	wg.Add(config.Threads)
	for i := 0; i < config.Threads; i++ {
		go func() {
			defer wg.Done()
			for key := range prefixs {
				if ctx.Err() != nil {
					return
				}
				if !strings.HasSuffix(key, "/") {
					if err := produceSingleObject(ctx, tasks, src, dst, key, config, checkpointMgr); err == nil {
						listedPrefix.Increment()
						continue
					} else if errors.Is(err, errDirSuffix) {
						key += "/"
					} else if os.IsNotExist(err) {
						atomic.AddInt64(&ignoreFiles, 1)
						listedPrefix.Increment()
						continue
					} else if ctx.Err() != nil {
						return
					}
				}
				logger.Debugf("start listing prefix %s", key)
				if restorePrefixFromCheckpoint(ctx, tasks, src, dst, key, config, checkpointMgr) {
					listedPrefix.Increment()
					continue
				}
				err := startProducer(ctx, tasks, src, dst, key, config.ListDepth, config, checkpointMgr)
				if err != nil && ctx.Err() == nil {
					logger.Errorf("list prefix %s: %s", key, err)
					failed.Increment()
				}
				listedPrefix.Increment()
			}
		}()
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		key := scanner.Text()
		if key == "" {
			continue
		}
		trimKey := strings.TrimRightFunc(key, unicode.IsSpace)
		if trimKey != key {
			logger.Infof("found a prefix with a space character:%q", key)
		}
		select {
		case prefixs <- trimKey:
		case <-ctx.Done():
		}
	}
	close(prefixs)

	wg.Wait()
	listedPrefix.Done()
	return ctx.Err()
}

var errDirSuffix = errors.New("dir miss suffix '/'")
var ignoreFiles int64

func produceSingleObject(ctx context.Context, tasks chan<- object.Object, src, dst object.ObjectStorage, key string, config *Config, checkpointMgr *CheckpointManager) error {
	obj, err := src.Head(ctx, key)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if config.Links && (errors.Is(err, utils.ErrExtlink) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, os.ErrNotExist)) {
			if sl, ok := src.(object.SupportSymlink); ok {
				if target, e := sl.Readlink(key); e == nil {
					obj, err = object.NewSymlink(key, target), nil
				} else {
					err = fmt.Errorf("readlink %s from %s: %w", key, src, e)
				}
			}
		}
		if err != nil {
			logger.Warnf("head %s from %s: %s", key, src, err)
			return err
		}
	}
	if obj.IsDir() && (!config.Links || !obj.IsSymlink()) {
		// only `files-from` will hit this case
		if !strings.HasSuffix(key, "/") {
			return errDirSuffix
		}
		if !config.Dirs {
			return nil
		}
	}
	var srckeys = make(chan object.Object, 1)
	srckeys <- obj
	close(srckeys)
	if dobj, e := dst.Head(ctx, key); e == nil || os.IsNotExist(e) {
		var dstkeys = make(chan object.Object, 1)
		if dobj != nil {
			dstkeys <- dobj
		}
		close(dstkeys)
		logger.Debugf("produce single key %s", key)
		return produce(ctx, tasks, srckeys, dstkeys, config, checkpointMgr, key)
	} else {
		if ctx.Err() == nil {
			logger.Warnf("head %s from %s: %s", key, dst, e)
		}
		err = e
	}
	return err
}

func restoreFromCheckpoint(ctx context.Context, tasks chan<- object.Object, src, dst object.ObjectStorage, config *Config, checkpointMgr *CheckpointManager) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(config.Threads, 1))
	prefixes := checkpointMgr.ListPrefixes()
	for _, prefix := range prefixes {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
		wg.Add(1)
		go func(prefix string) {
			defer wg.Done()
			defer func() { <-sem }()
			restorePrefixFromCheckpoint(ctx, tasks, src, dst, prefix, config, checkpointMgr)
		}(prefix)
	}
	wg.Wait()
	return ctx.Err()
}

func startProducer(ctx context.Context, tasks chan<- object.Object, src, dst object.ObjectStorage, prefix string, listDepth int, config *Config, checkpointMgr *CheckpointManager) error {
	select {
	case config.concurrentList <- 1:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		<-config.concurrentList
	}()
	if config.Limit == 1 && len(config.rules) == 0 {
		if produceSingleObject(ctx, tasks, src, dst, prefix, config, checkpointMgr) == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if config.ListThreads <= 1 || listDepth <= 0 {
		return startSingleProducer(ctx, tasks, src, dst, prefix, config, checkpointMgr)
	}

	commonPrefix := make(chan object.Object, 1000)
	startAfter := checkpointMgr.GetLastListedKey(prefix)

	var onChildPrefix func(string)
	if checkpointMgr != nil {
		onChildPrefix = func(key string) {
			if (len(config.rules) == 0 || matchLeveledPath(config.rules, key)) &&
				key >= config.Start &&
				(config.End == "" || key <= config.End) {
				checkpointMgr.RegisterChildPrefix(key, listDepth-1)
			}
		}
	}

	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	srckeys, err := listCommonPrefix(listCtx, src, prefix, commonPrefix, !config.Links, startAfter, onChildPrefix, config.ExternalSortDir)
	if err == utils.ErrNotSUP {
		return startSingleProducer(ctx, tasks, src, dst, prefix, config, checkpointMgr)
	} else if err != nil {
		return fmt.Errorf("list %s with delimiter: %w", src, err)
	}
	var dcp chan object.Object
	if config.DeleteDst {
		dcp = commonPrefix // search common prefix in dst
	}
	var dstkeys <-chan object.Object
	if config.ForceUpdate && !config.DeleteDst {
		t := make(chan object.Object)
		close(t)
		dstkeys = t
	} else {
		dstkeys, err = listCommonPrefix(listCtx, dst, prefix, dcp, !config.Links, startAfter, nil, config.ExternalSortDir)
		if err != nil {
			cancel()
			for range srckeys {
			}
			if err == utils.ErrNotSUP {
				return startSingleProducer(ctx, tasks, src, dst, prefix, config, checkpointMgr)
			}
			return fmt.Errorf("list %s with delimiter: %w", dst, err)
		}
	}

	done := make(chan bool)
	go func() {
		defer close(done)
		var mu sync.Mutex
		processing := make(map[string]bool)
		var wg sync.WaitGroup
		defer wg.Wait()
		for c := range commonPrefix {
			if ctx.Err() != nil {
				continue
			}
			mu.Lock()
			if processing[c.Key()] {
				mu.Unlock()
				continue
			}
			processing[c.Key()] = true
			mu.Unlock()

			if len(config.rules) > 0 && !matchLeveledPath(config.rules, c.Key()) {
				logger.Infof("exclude prefix %s", c.Key())
				continue
			}
			if c.Key() < config.Start {
				logger.Infof("ignore prefix %s", c.Key())
				continue
			}
			if config.End != "" && c.Key() > config.End {
				logger.Infof("ignore prefix %s", c.Key())
				continue
			}
			wg.Add(1)
			go func(prefix string) {
				defer wg.Done()
				// In the produceFromList, only the top-level prefix from the file gets restored.
				// Child prefixes in the checkpoint must be restored here, otherwise their pending/failed keys are lost.
				if restorePrefixFromCheckpoint(ctx, tasks, src, dst, prefix, config, checkpointMgr) {
					return
				}
				err := startProducer(ctx, tasks, src, dst, prefix, listDepth-1, config, checkpointMgr)
				if err != nil && ctx.Err() == nil {
					logger.Errorf("list prefix %s: %s", prefix, err)
					failed.Increment()
				}
			}(c.Key())
		}
	}()

	// sync returned objects
	if err := produce(ctx, tasks, srckeys, dstkeys, config, checkpointMgr, prefix); err != nil {
		cancel()
		for range srckeys {
		}
		for range dstkeys {
		}
		close(commonPrefix)
		<-config.concurrentList
		<-done
		config.concurrentList <- 1
		return err
	}
	// consume all the keys from dst
	for range dstkeys {
	}
	close(commonPrefix)

	<-config.concurrentList
	<-done
	config.concurrentList <- 1
	return ctx.Err()
}

// Sync syncs all the keys between to object storage
func Sync(ctx context.Context, src, dst object.ObjectStorage, config *Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var checkpointMgr *CheckpointManager
	var checkpoint *Checkpoint
	var uploads multipartUploads
	var workerUploads *workerMultipartUploads
	var maxFailureReached atomic.Bool

	if config.EnableCheckpoint {
		if config.Manager == "" {
			checkpointMgr = NewCheckpointManager(src, dst, config)
			uploads = checkpointMgr
			if config.CheckpointForceReset {
				if err := checkpointMgr.DeleteCheckpoint(ctx); err != nil && !errors.Is(err, os.ErrNotExist) {
					logger.Warnf("Failed to delete existing checkpoint: %v", err)
				}
				checkpointMgr.Reset(config)
				logger.Infof("Force reset checkpoint, starting fresh")
			} else if ckpt, err := checkpointMgr.Load(ctx); err == nil {
				if checkpointMgr.ValidateConfig(config) {
					if len(ckpt.PrefixState) > 0 || len(ckpt.MultipartUploads) > 0 || len(ckpt.SrcDelayDel) > 0 || len(ckpt.DstDelayDel) > 0 {
						checkpoint = ckpt
						config.Limit = ckpt.Config.Limit
						ckpt.Config = config
						if len(ckpt.SrcDelayDel) > 0 {
							logger.Infof("Checkpoint has %d pending deletes in source", len(ckpt.SrcDelayDel))
							srcDelayDelMu.Lock()
							srcDelayDel = append([]string(nil), ckpt.SrcDelayDel...)
							srcDelayDelMu.Unlock()
						}
						if len(ckpt.DstDelayDel) > 0 {
							dstDelayDelMu.Lock()
							dstDelayDel = append([]string(nil), ckpt.DstDelayDel...)
							dstDelayDelMu.Unlock()
						}
						logger.Infof("Loaded checkpoint from %s", ckpt.UpdatedAt.Format(time.RFC3339))
					} else {
						logger.Infof("Loaded empty checkpoint, starting fresh")
					}
				} else {
					logger.Warnf("Checkpoint config mismatch, starting fresh")
					checkpointMgr.Reset(config)
				}
			} else {
				if !errors.Is(err, os.ErrNotExist) {
					logger.Warnf("Failed to load checkpoint: %v", err)
				} else {
					logger.Infof("No checkpoint found, starting fresh")
				}
			}

			checkpointMgr.StartPeriodicSave(ctx, config.CheckpointInterval)
		} else {
			workerUploads = newWorkerMultipartUploads()
			uploads = workerUploads
		}
	}
	if strings.HasPrefix(src.String(), "file://") && strings.HasPrefix(dst.String(), "file://") {
		major, minor := utils.GetKernelVersion()
		// copy_file_range() system call first appeared in Linux 4.5, and reworked in 5.3
		// Go requires kernel >= 5.3 to use copy_file_range(), see:
		// https://github.com/golang/go/blob/go1.17.11/src/internal/poll/copy_file_range_linux.go#L58-L66
		if major > 5 || (major == 5 && minor >= 3) {
			d1 := utils.GetDev(src.String()[7:]) // remove prefix "file://"
			d2 := utils.GetDev(dst.String()[7:])
			if d1 != -1 && d1 == d2 {
				object.TryCFR = true
			}
		}
	}

	if config.Inplace {
		object.PutInplace = true
	}

	var bufferSize = 10240
	if config.Manager != "" {
		// No support for work-stealing, so workers shouldnot buffer tasks to prevent piling up in their own queues, which could cause imbalance among workers.
		bufferSize = 1
	}
	tasks := make(chan object.Object, bufferSize)
	wg := sync.WaitGroup{}
	concurrent = make(chan int, config.Threads)
	var localLimit *ratelimit.Bucket
	if config.BWLimit > 0 {
		bps := float64(config.BWLimit*1e6/8) * 0.85 // 15% overhead
		localLimit = ratelimit.NewBucketWithRate(bps, int64(bps)/10)
	}
	var gLimit *globalLimit
	if config.TrafficControlURL != "" {
		gLimit = &globalLimit{address: config.TrafficControlURL, localBW: config.BWLimit}
		gLimit.healthy.Store(true)
		go func() {
			ticker := time.NewTicker(time.Millisecond * 10)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					gLimit.checkBalance(ctx)
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	if localLimit != nil || gLimit != nil {
		limiter = &mixedLimiter{
			global: gLimit,
			local:  localLimit,
		}
	}

	progress := utils.NewProgress(config.Verbose || config.Quiet || config.Manager != "")
	handled = progress.AddCountBar("Scanned objects", 0)
	excluded = progress.AddCountSpinner("Excluded objects")
	excludedBytes = progress.AddByteSpinner("Excluded bytes")
	skipped = progress.AddCountSpinner("Skipped objects")
	skippedBytes = progress.AddByteSpinner("Skipped bytes")
	extra = progress.AddCountSpinner("Extra objects")
	extraBytes = progress.AddByteSpinner("Extra bytes")
	pending = progress.AddCountSpinner("Pending objects")
	copied = progress.AddCountSpinner("Copied objects")
	copiedBytes = progress.AddByteSpinner("Copied bytes")
	if config.CheckAll || config.CheckNew || config.CheckChange {
		checked = progress.AddCountSpinner("Checked objects")
		checkedBytes = progress.AddByteSpinner("Checked bytes")
	}
	if config.DeleteSrc || config.DeleteDst || config.DeleteSrcAfter {
		deleted = progress.AddCountSpinner("Deleted objects")
	}

	if checkpoint != nil {
		copied.SetCurrent(checkpoint.Stats.Copied)
		copiedBytes.SetCurrent(checkpoint.Stats.CopiedBytes)
		if checked != nil {
			checked.SetCurrent(checkpoint.Stats.Checked)
			checkedBytes.SetCurrent(checkpoint.Stats.CheckedBytes)
		}
		if deleted != nil {
			deleted.SetCurrent(checkpoint.Stats.Deleted)
		}
		skipped.SetCurrent(checkpoint.Stats.Skipped)
		skippedBytes.SetCurrent(checkpoint.Stats.SkippedBytes)
		// Restore handled bar: total = current = checkpoint value (scanned = handled)
		handled.SetTotal(checkpoint.Stats.Handled)
		handled.SetCurrent(checkpoint.Stats.Handled)
	}

	if checkpointMgr != nil {
		checkpointMgr.statsUpdater = func(stats *CheckpointStats) {
			stats.Copied = copied.Current()
			stats.CopiedBytes = copiedBytes.Current()
			if checked != nil {
				stats.Checked = checked.Current()
				stats.CheckedBytes = checkedBytes.Current()
			}
			if deleted != nil {
				stats.Deleted = deleted.Current()
			}
			stats.Skipped = skipped.Current()
			stats.SkippedBytes = skippedBytes.Current()
			stats.Handled = handled.Current()
			stats.Failed = 0
			checkpointMgr.checkpoint.RLock()
			for _, state := range checkpointMgr.checkpoint.PrefixState {
				state.RLock()
				stats.Failed += int64(len(state.FailedKeys))
				state.RUnlock()
			}
			checkpointMgr.checkpoint.RUnlock()
		}
	}

	syncExitFunc := func() error {
		if config.Manager == "" {
			val := atomic.LoadInt64(&ignoreFiles)
			if val > 0 {
				logger.Infof("Ignored %d non-existent paths from the file list", val)
			}
			pending.SetCurrent(0)
			incrHandled(0)
			total := handled.GetTotal()
			progress.Done()

			msg := fmt.Sprintf("Found: %d, excluded: %d (%s), skipped: %d (%s), copied: %d (%s), extra: %d (%s)", total,
				excluded.Current(), formatSize(excludedBytes.Current()),
				skipped.Current(), formatSize(skippedBytes.Current()),
				copied.Current(), formatSize(copiedBytes.Current()),
				extra.Current(), formatSize(extraBytes.Current()))
			if checked != nil {
				msg += fmt.Sprintf(", checked: %d (%s)", checked.Current(), formatSize(checkedBytes.Current()))
			}
			if deleted != nil {
				msg += fmt.Sprintf(", deleted: %d", deleted.Current())
			}
			if failed != nil {
				msg += fmt.Sprintf(", failed: %d", failed.Current())
			}
			if total-handled.Current() > 0 {
				msg += fmt.Sprintf(", lost: %d", total-handled.Current())
			}
			logger.Info(msg)

			if ctx.Err() != nil {
				if checkpointMgr != nil {
					cCtx, cCancel := cleanupCtx(ctx)
					if e := checkpointMgr.Save(cCtx, checkpointMgr.checkpoint); e != nil {
						logger.Warnf("Failed to save checkpoint on cancel: %v", e)
					} else {
						logger.Infof("Checkpoint saved successfully")
					}
					cCancel()
				}
				if maxFailureReached.Load() {
					return fmt.Errorf("the maximum error limit of %d was reached", config.MaxFailure)
				}
				return ctx.Err()
			}

			if failed != nil {
				if n := failed.Current(); n > 0 || total > handled.Current() {
					if checkpointMgr != nil {
						cCtx, cCancel := cleanupCtx(ctx)
						if e := checkpointMgr.Save(cCtx, checkpointMgr.checkpoint); e != nil {
							logger.Warnf("Failed to save checkpoint after failure: %v", e)
						}
						cCancel()
					}
					return fmt.Errorf("failed to handle %d objects", n+total-handled.Current())
				}
			}
			if checkpointMgr != nil && !config.Dry {
				if e := try(ctx, 3, func() error {
					return checkpointMgr.DeleteCheckpoint(ctx)
				}); e != nil {
					logger.Warnf("Failed to delete checkpoint after completion: %v", e)
				}
			}
		} else {
			cCtx, cCancel := cleanupCtx(ctx)
			defer cCancel()
			if sendStats(cCtx, config.Manager, workerUploads) {
				for len(srcDelayDel) > 0 && cCtx.Err() == nil {
					if !sendStats(cCtx, config.Manager, workerUploads) {
						break
					}
				}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Infof("This worker process has already completed its tasks")
		}
		return nil
	}

	if !config.Dry {
		failed = progress.AddCountSpinner("Failed objects")
		if config.MaxFailure > 0 {
			go func() {
				ticker := time.NewTicker(time.Millisecond * 100)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if failed.Current() >= config.MaxFailure {
							logger.Infof("the maximum error limit of %d was reached, stop now", config.MaxFailure)
							maxFailureReached.Store(true)
							cancel()
							return
						}
					case <-ctx.Done():
						return
					}
				}
			}()
		}
	}

	if config.Manager == "" && config.FilesFrom != "" {
		listedPrefix = progress.AddCountSpinner("Prefix")
	}

	go func() {
		ticker := time.NewTicker(time.Millisecond * 100)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pending.SetCurrent(int64(len(tasks)))
			case <-ctx.Done():
				return
			}
		}
	}()

	initSyncMetrics(config)
	for i := 0; i < config.Threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker(ctx, tasks, src, dst, config, checkpointMgr, uploads)
		}()
	}

	if len(config.Exclude) > 0 {
		config.rules = parseIncludeRules(os.Args)
	}

	var producerErr error
	if config.Manager == "" {
		if len(config.Workers) > 0 {
			addr, err := startManager(ctx, config, tasks, checkpointMgr)
			if err != nil {
				cancel()
				close(tasks)
				wg.Wait()
				if checkpointMgr != nil {
					checkpointMgr.Stop()
				}
				return err
			}
			launchWorker(ctx, addr, config, &wg)
		}
		logger.Infof("Syncing from %q to %q", src, dst)
		if config.Start != "" {
			logger.Infof("first key: %q", config.Start)
		}
		if config.End != "" {
			logger.Infof("last key: %q", config.End)
		}
		config.concurrentList = make(chan int, config.ListThreads)

		if config.FilesFrom != "" {
			producerErr = produceFromList(ctx, tasks, src, dst, config, checkpointMgr)
		} else if checkpoint != nil {
			producerErr = restoreFromCheckpoint(ctx, tasks, src, dst, config, checkpointMgr)
		} else {
			producerErr = startProducer(ctx, tasks, src, dst, "", config.ListDepth, config, checkpointMgr)
		}
		close(tasks)
	} else {
		go fetchJobs(ctx, tasks, config, uploads)
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if !sendStats(ctx, config.Manager, workerUploads) {
						cancel()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	wg.Wait()

	if checkpointMgr != nil {
		checkpointMgr.Stop()
	}

	if producerErr != nil && ctx.Err() == nil {
		if checkpointMgr != nil {
			cCtx, cCancel := cleanupCtx(ctx)
			if e := checkpointMgr.Save(cCtx, checkpointMgr.checkpoint); e != nil {
				logger.Warnf("Failed to save checkpoint after producer error: %v", e)
			}
			cCancel()
		}
		progress.Done()
		return producerErr
	}

	if config.Manager == "" && ctx.Err() == nil {
		delayDelFunc := func(storage object.ObjectStorage, keys []string) {
			if len(keys) > 0 {
				logger.Infof("delete %d dirs from %s", len(keys), storage)
				sort.Strings(keys)
			}
			for i := len(keys) - 1; i >= 0; i-- {
				if ctx.Err() != nil {
					return
				}
				incrHandled(1)
				_ = deleteObjWithLimit(ctx, storage, keys[i], config.Dry, config.Limit)
			}
		}
		delWg := sync.WaitGroup{}

		delWg.Add(1)
		go func() {
			delayDelFunc(src, srcDelayDel)
			delWg.Done()
		}()
		delWg.Add(1)
		go func() {
			delayDelFunc(dst, dstDelayDel)
			delWg.Done()
		}()
		delWg.Wait()
	}
	return syncExitFunc()
}

func initSyncMetrics(config *Config) {
	if config.Registerer != nil {
		config.Registerer.MustRegister(
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "scanned",
				Help: "Scanned objects",
			}, func() float64 {
				return float64(handled.Total())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "excluded",
				Help: "Excluded objects",
			}, func() float64 {
				return float64(excluded.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "excluded_bytes",
				Help: "Excluded bytes",
			}, func() float64 {
				return float64(excludedBytes.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "extra",
				Help: "Extra objects",
			}, func() float64 {
				return float64(extra.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "extra_bytes",
				Help: "Extra bytes",
			}, func() float64 {
				return float64(extraBytes.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "handled",
				Help: "Handled objects",
			}, func() float64 {
				return float64(handled.Current())
			}),
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "pending",
				Help: "Pending objects",
			}, func() float64 {
				return float64(pending.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "copied",
				Help: "Copied objects",
			}, func() float64 {
				return float64(copied.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "copied_bytes",
				Help: "Copied bytes",
			}, func() float64 {
				return float64(copiedBytes.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "skipped",
				Help: "Skipped objects",
			}, func() float64 {
				return float64(skipped.Current())
			}),
			prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "skipped_bytes",
				Help: "Skipped bytes",
			}, func() float64 {
				return float64(skippedBytes.Current())
			}),
		)
		if failed != nil {
			config.Registerer.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "failed",
				Help: "Failed objects",
			}, func() float64 {
				return float64(failed.Current())
			}))
		}
		if deleted != nil {
			config.Registerer.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "deleted",
				Help: "Deleted objects",
			}, func() float64 {
				return float64(deleted.Current())
			}))
		}
		if checked != nil && checkedBytes != nil {
			config.Registerer.MustRegister(
				prometheus.NewCounterFunc(prometheus.CounterOpts{
					Name: "checked",
					Help: "Checked objects",
				}, func() float64 {
					return float64(checked.Current())
				}),
				prometheus.NewCounterFunc(prometheus.CounterOpts{
					Name: "checked_bytes",
					Help: "Checked bytes",
				}, func() float64 {
					return float64(checkedBytes.Current())
				}))
		}
		if listedPrefix != nil {
			config.Registerer.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
				Name: "listed_prefix",
				Help: "listed prefix",
			}, func() float64 {
				return float64(listedPrefix.Current())
			}))
		}
	}
}
