/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package meta

import (
	"fmt"
	"net/url"
	"path"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/redis/go-redis/v9"
)

const (
	aclCounter     = "aclMaxId"
	usedSpace      = "usedSpace"
	totalInodes    = "totalInodes"
	legacySessions = "sessions"
)

var counterNames = []string{usedSpace, totalInodes, "nextInode", "nextChunk", "nextSession", "nextTrash"}

const (
	// fallocate
	fallocKeepSize  = 0x01
	fallocPunchHole = 0x02
	// RESERVED: fallocNoHideStale   = 0x04
	fallocCollapesRange = 0x08
	fallocZeroRange     = 0x10
	fallocInsertRange   = 0x20
)
const (
	// clone mode
	CLONE_MODE_CAN_OVERWRITE      = 0x01
	CLONE_MODE_PRESERVE_ATTR      = 0x02
	CLONE_MODE_PRESERVE_HARDLINKS = 0x08

	// atime mode
	NoAtime     = "noatime"
	RelAtime    = "relatime"
	StrictAtime = "strictatime"
)

const (
	MODE_MASK_R = 0b100
	MODE_MASK_W = 0b010
	MODE_MASK_X = 0b001
)

type msgCallbacks struct {
	sync.Mutex
	callbacks map[uint32]MsgCallback
}

type freeID struct {
	next  uint64
	maxid uint64
}

var logger = utils.GetLogger("juicefs")

type queryMap struct {
	*url.Values
}

func (qm *queryMap) duration(key, originalKey string, d time.Duration) time.Duration {
	val := qm.Get(key)
	if val == "" {
		oVal := qm.Get(originalKey)
		if oVal == "" {
			return d
		}
		val = oVal
	}

	qm.Del(key)
	if dur, err := time.ParseDuration(val); err == nil {
		return dur
	} else {
		logger.Warnf("Parse duration %s for key %s: %s", val, key, err)
		return d
	}
}

func (qm *queryMap) pop(key string) string {
	defer qm.Del(key)
	return qm.Get(key)
}

func errno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if eno, ok := err.(syscall.Errno); ok {
		return eno
	}
	if err == redis.Nil {
		return syscall.ENOENT
	}
	if strings.HasPrefix(err.Error(), "OOM") {
		return syscall.ENOSPC
	}
	logger.Errorf("error: %s\n%s", err, debug.Stack())
	return syscall.EIO
}

func accessMode(attr *Attr, uid uint32, gids []uint32) uint8 {
	if uid == 0 {
		return 0x7
	}
	mode := attr.Mode
	if uid == attr.Uid {
		return uint8(mode>>6) & 7
	}
	for _, gid := range gids {
		if gid == attr.Gid {
			return uint8(mode>>3) & 7
		}
	}
	return uint8(mode & 7)
}

func align4K(length uint64) int64 {
	if length == 0 {
		return 1 << 12
	}
	return int64((((length - 1) >> 12) + 1) << 12)
}

type plockRecord struct {
	Type  uint32
	Pid   uint32
	Start uint64
	End   uint64
}

type ownerKey struct {
	Sid   uint64
	Owner uint64
}

type PLockItem struct {
	ownerKey
	plockRecord
}

type FLockItem struct {
	ownerKey
	Type string
}

func parseOwnerKey(key string) (*ownerKey, error) {
	pair := strings.Split(key, "_")
	if len(pair) != 2 {
		return nil, fmt.Errorf("invalid owner key: %s", key)
	}
	sid, err := strconv.ParseUint(pair[0], 10, 64)
	if err != nil {
		return nil, err
	}
	owner, err := strconv.ParseUint(pair[1], 16, 64)
	if err != nil {
		return nil, err
	}
	return &ownerKey{sid, owner}, nil
}

func loadLocks(d []byte) []plockRecord {
	var ls []plockRecord
	rb := utils.FromBuffer(d)
	for rb.HasMore() {
		ls = append(ls, plockRecord{rb.Get32(), rb.Get32(), rb.Get64(), rb.Get64()})
	}
	return ls
}

func dumpLocks(ls []plockRecord) []byte {
	wb := utils.NewBuffer(uint32(len(ls)) * 24)
	for _, l := range ls {
		wb.Put32(l.Type)
		wb.Put32(l.Pid)
		wb.Put64(l.Start)
		wb.Put64(l.End)
	}
	return wb.Bytes()
}

func updateLocks(ls []plockRecord, nl plockRecord) []plockRecord {
	// ls is ordered by l.start without overlap
	size := len(ls)
	for i := 0; i < size && nl.Start <= nl.End; i++ {
		l := ls[i]
		if nl.Start < l.Start && nl.End >= l.Start {
			// split nl
			ls = append(ls, nl)
			ls[len(ls)-1].End = l.Start - 1
			nl.Start = l.Start
		}
		if nl.Start > l.Start && nl.Start <= l.End {
			// split l
			l.End = nl.Start - 1
			ls = append(ls, l)
			ls[i].Start = nl.Start
			l = ls[i]
		}
		if nl.Start == l.Start {
			ls[i].Type = nl.Type // update l
			ls[i].Pid = nl.Pid
			if l.End > nl.End {
				// split l
				ls[i].End = nl.End
				l.Start = nl.End + 1
				ls = append(ls, l)
			}
			nl.Start = ls[i].End + 1
		}
	}
	if nl.Start <= nl.End {
		ls = append(ls, nl)
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].Start < ls[j].Start })
	for i := 0; i < len(ls); {
		if ls[i].Type == F_UNLCK || ls[i].Start > ls[i].End {
			// remove empty one
			copy(ls[i:], ls[i+1:])
			ls = ls[:len(ls)-1]
		} else {
			if i+1 < len(ls) && ls[i].Type == ls[i+1].Type && ls[i].Pid == ls[i+1].Pid && ls[i].End+1 == ls[i+1].Start {
				// combine continuous range
				ls[i].End = ls[i+1].End
				ls[i+1].Start = ls[i+1].End + 1
			}
			i++
		}
	}
	return ls
}

func (m *baseMeta) emptyDir(ctx Context, inode Ino, skipCheckTrash bool, count *uint64, concurrent chan int) syscall.Errno {
	for {
		var entries []*Entry
		if st := m.en.doReaddir(ctx, inode, 0, &entries, 10000); st != 0 && st != syscall.ENOENT {
			return st
		}
		if len(entries) == 0 {
			return 0
		}
		if st := m.Access(ctx, inode, MODE_MASK_W|MODE_MASK_X, nil); st != 0 {
			return st
		}
		var wg sync.WaitGroup
		var status syscall.Errno
		// try directories first to increase parallel
		var dirs int
		for i, e := range entries {
			if e.Attr.Typ == TypeDirectory {
				entries[dirs], entries[i] = entries[i], entries[dirs]
				dirs++
			}
		}
		for i, e := range entries {
			if e.Attr.Typ == TypeDirectory {
				select {
				case concurrent <- 1:
					wg.Add(1)
					go func(child Ino, name string) {
						defer wg.Done()
						st := m.emptyEntry(ctx, inode, name, child, skipCheckTrash, count, concurrent)
						if st != 0 && st != syscall.ENOENT {
							status = st
						}
						<-concurrent
					}(e.Inode, string(e.Name))
				default:
					if st := m.emptyEntry(ctx, inode, string(e.Name), e.Inode, skipCheckTrash, count, concurrent); st != 0 && st != syscall.ENOENT {
						ctx.Cancel()
						return st
					}
				}
			} else {
				if count != nil {
					atomic.AddUint64(count, 1)
				}
				if st := m.Unlink(ctx, inode, string(e.Name), skipCheckTrash); st != 0 && st != syscall.ENOENT {
					ctx.Cancel()
					return st
				}
			}
			if ctx.Canceled() {
				return syscall.EINTR
			}
			entries[i] = nil // release memory
		}
		wg.Wait()
		if status != 0 || inode == TrashInode { // try only once for .trash
			return status
		}
	}
}

func (m *baseMeta) emptyEntry(ctx Context, parent Ino, name string, inode Ino, skipCheckTrash bool, count *uint64, concurrent chan int) syscall.Errno {
	st := m.emptyDir(ctx, inode, skipCheckTrash, count, concurrent)
	if st == 0 && !isTrash(inode) {
		st = m.Rmdir(ctx, parent, name, skipCheckTrash)
		if st == syscall.ENOTEMPTY {
			// redo when concurrent conflict may happen
			st = m.emptyEntry(ctx, parent, name, inode, skipCheckTrash, count, concurrent)
		} else if count != nil {
			atomic.AddUint64(count, 1)
		}
	}
	return st
}

func (m *baseMeta) Remove(ctx Context, parent Ino, name string, skipTrash bool, numThreads int, count *uint64) syscall.Errno {
	parent = m.checkRoot(parent)
	if st := m.Access(ctx, parent, MODE_MASK_W|MODE_MASK_X, nil); st != 0 {
		return st
	}
	var inode Ino
	var attr Attr
	if st := m.Lookup(ctx, parent, name, &inode, &attr, false); st != 0 {
		return st
	}
	if attr.Typ != TypeDirectory {
		if count != nil {
			atomic.AddUint64(count, 1)
		}
		return m.Unlink(ctx, parent, name)
	}
	if numThreads <= 0 {
		logger.Infof("invalid threads number %d , auto adjust to %d", numThreads, RmrDefaultThreads)
		numThreads = RmrDefaultThreads
	} else if numThreads > 255 {
		logger.Infof("threads number %d too large, auto adjust to 255 .", numThreads)
		numThreads = 255
	}
	logger.Debugf("Start emptyEntry with %d concurrent threads .", numThreads)
	concurrent := make(chan int, numThreads)
	return m.emptyEntry(ctx, parent, name, inode, skipTrash, count, concurrent)
}

func (m *baseMeta) GetSummary(ctx Context, inode Ino, summary *Summary, recursive bool, strict bool) syscall.Errno {
	var attr Attr
	if st := m.GetAttr(ctx, inode, &attr); st != 0 {
		return st
	}
	if attr.Typ != TypeDirectory {
		if attr.Typ == TypeDirectory {
			summary.Dirs++
		} else {
			summary.Files++
		}
		summary.Size += uint64(align4K(attr.Length))
		if attr.Typ == TypeFile {
			summary.Length += attr.Length
		}
		return 0
	}
	summary.Dirs++
	summary.Size += uint64(align4K(0))
	concurrent := make(chan struct{}, 50)
	inode = m.checkRoot(inode)
	return m.getDirSummary(ctx, inode, summary, recursive, strict, concurrent, nil)
}

func (m *baseMeta) getDirSummary(ctx Context, inode Ino, summary *Summary, recursive bool, strict bool, concurrent chan struct{}, updateProgress func(count uint64, bytes uint64)) syscall.Errno {
	var entries []*Entry
	var err syscall.Errno
	format := m.getFormat()
	if strict || !format.DirStats {
		err = m.en.doReaddir(ctx, inode, 1, &entries, -1)
	} else {
		var st *dirStat
		st, err = m.GetDirStat(ctx, inode)
		if err != 0 {
			return err
		}
		atomic.AddUint64(&summary.Size, uint64(st.space))
		atomic.AddUint64(&summary.Length, uint64(st.length))
		if updateProgress != nil {
			updateProgress(uint64(st.inodes), uint64(st.space))
		}
		var attr Attr
		err = m.en.doGetAttr(ctx, inode, &attr)
		if err == 0 {
			if attr.Nlink > 2 {
				err = m.en.doReaddir(ctx, inode, 0, &entries, -1)
			} else {
				atomic.AddUint64(&summary.Files, uint64(st.inodes))
			}
		}
	}
	if err != 0 {
		return err
	}

	var wg sync.WaitGroup
	var errCh = make(chan syscall.Errno, 1)
	for _, e := range entries {
		if e.Attr.Typ == TypeDirectory {
			atomic.AddUint64(&summary.Dirs, 1)
		} else {
			atomic.AddUint64(&summary.Files, 1)
		}
		if strict || !format.DirStats {
			atomic.AddUint64(&summary.Size, uint64(align4K(e.Attr.Length)))
			if e.Attr.Typ == TypeFile {
				atomic.AddUint64(&summary.Length, e.Attr.Length)
			}
			if updateProgress != nil {
				updateProgress(1, uint64(align4K(e.Attr.Length)))
			}
		}
		if e.Attr.Typ != TypeDirectory || !recursive {
			continue
		}
		select {
		case <-ctx.Done():
			return syscall.EINTR
		case err := <-errCh:
			// TODO: cancel others
			return err
		case concurrent <- struct{}{}:
			wg.Add(1)
			go func(e *Entry) {
				defer wg.Done()
				err := m.getDirSummary(ctx, e.Inode, summary, recursive, strict, concurrent, updateProgress)
				<-concurrent
				if err != 0 && err != syscall.ENOENT {
					select {
					case errCh <- err:
					default:
					}
				}
			}(e)
		default:
			if err := m.getDirSummary(ctx, e.Inode, summary, recursive, strict, concurrent, updateProgress); err != 0 && err != syscall.ENOENT {
				return err
			}
		}
	}
	wg.Wait()
	select {
	case err = <-errCh:
	default:
	}
	return err
}

func (m *baseMeta) GetTreeSummary(ctx Context, root *TreeSummary, depth, topN uint8, strict bool,
	updateProgress func(count uint64, bytes uint64)) syscall.Errno {
	var attr Attr
	if st := m.GetAttr(ctx, root.Inode, &attr); st != 0 {
		return st
	}
	if updateProgress != nil {
		updateProgress(1, uint64(align4K(0)))
	}
	if attr.Typ != TypeDirectory {
		root.Files++
		root.Size += uint64(align4K(attr.Length))
		return 0
	}
	root.Dirs++
	root.Size += uint64(align4K(0))
	concurrent := make(chan struct{}, 50)
	root.Inode = m.checkRoot(root.Inode)
	return m.getTreeSummary(ctx, root, depth, topN, strict, concurrent, updateProgress)
}

func (m *baseMeta) getTreeSummary(ctx Context, tree *TreeSummary, depth, topN uint8, strict bool, concurrent chan struct{},
	updateProgress func(count uint64, bytes uint64)) syscall.Errno {
	if depth <= 0 {
		var summary Summary
		err := m.getDirSummary(ctx, tree.Inode, &summary, true, strict, concurrent, updateProgress)
		if err == 0 {
			tree.Dirs += summary.Dirs
			tree.Files += summary.Files
			tree.Size += summary.Size
		}
		return err
	}

	var entries []*Entry
	if err := m.en.doReaddir(ctx, tree.Inode, 1, &entries, -1); err != 0 {
		return err
	}
	var wg sync.WaitGroup
	tree.Children = make([]*TreeSummary, len(entries))
	errCh := make(chan syscall.Errno, 1)
	var err syscall.Errno
	for i, e := range entries {
		child := &TreeSummary{
			Inode: e.Inode,
			Path:  path.Join(tree.Path, string(e.Name)),
			Type:  e.Attr.Typ,
			Size:  uint64(align4K(e.Attr.Length)),
		}
		tree.Children[i] = child
		if updateProgress != nil {
			updateProgress(1, uint64(align4K(e.Attr.Length)))
		}
		if e.Attr.Typ != TypeDirectory {
			child.Files++
			continue
		}
		child.Dirs++
		select {
		case <-ctx.Done():
			return syscall.EINTR
		case err = <-errCh:
			return err
		case concurrent <- struct{}{}:
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := m.getTreeSummary(ctx, child, depth-1, topN, strict, concurrent, updateProgress)
				<-concurrent
				if err != 0 && err != syscall.ENOENT {
					select {
					case errCh <- err:
					default:
					}
				}
			}()
		default:
			if err = m.getTreeSummary(ctx, child, depth-1, topN, strict, concurrent, updateProgress); err != 0 && err != syscall.ENOENT {
				return err
			}
		}
	}
	wg.Wait()
	select {
	case err = <-errCh:
		return err
	default:
	}

	// pick top N
	for _, c := range tree.Children {
		tree.Dirs += c.Dirs
		tree.Files += c.Files
		tree.Size += c.Size
	}
	sort.Slice(tree.Children, func(i, j int) bool {
		return tree.Children[i].Size > tree.Children[j].Size
	})
	if len(tree.Children) > int(topN) {
		omitChild := &TreeSummary{
			Path: path.Join(tree.Path, "..."),
			Type: TypeFile,
		}
		for _, child := range tree.Children[topN:] {
			omitChild.Size += child.Size
			omitChild.Files += child.Files
			omitChild.Dirs += child.Dirs
		}
		tree.Children = append(tree.Children[:topN], omitChild)
	}
	return 0
}

func (m *baseMeta) atimeNeedsUpdate(attr *Attr, now time.Time) bool {
	return m.conf.AtimeMode != NoAtime && relatimeNeedUpdate(attr, now) ||
		// update atime only for > 1 second accesses
		m.conf.AtimeMode == StrictAtime && now.Sub(time.Unix(attr.Atime, int64(attr.Atimensec))) > time.Second
}

// With relative atime, only update atime if the previous atime is earlier than either the ctime or
// mtime or if at least a day has passed since the last atime update.
func relatimeNeedUpdate(attr *Attr, now time.Time) bool {
	atime := time.Unix(attr.Atime, int64(attr.Atimensec))
	mtime := time.Unix(attr.Mtime, int64(attr.Mtimensec))
	ctime := time.Unix(attr.Ctime, int64(attr.Ctimensec))
	return mtime.After(atime) || ctime.After(atime) || now.Sub(atime) > 24*time.Hour
}
