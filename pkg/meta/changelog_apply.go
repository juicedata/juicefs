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

package meta

import (
	"fmt"
	"syscall"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
)

type changelogApplyStateKey struct{}

type changelogApplyState struct {
	Time    time.Time
	Inode   Ino
	Inodes  []Ino        // Source inodes of a batch operation, consumed in order.
	TokenId uint32       // Source token ID for STORETOKEN.
	Trash   Ino          // Source trash directory; zero skips trash.
	Sid     uint64       // Source session ID for sustained inodes.
	Mode    *uint16      // Source mode of a created or re-permissioned inode.
	Parents map[Ino]bool // Whether the source updated each parent directory.
	Opened  map[Ino]bool // Whether each inode was open on the source when removed.
}

func getChangelogApplyState(ctx Context) *changelogApplyState {
	s, _ := ctx.Value(changelogApplyStateKey{}).(*changelogApplyState)
	return s
}

func isApplyMode(ctx Context) bool {
	return getChangelogApplyState(ctx) != nil
}

func operationTime(ctx Context) time.Time {
	if s := getChangelogApplyState(ctx); s != nil {
		return s.Time
	}
	return time.Now()
}

func applyParent(ctx Context, parent Ino, update bool) bool {
	if s := getChangelogApplyState(ctx); s != nil {
		if v, ok := s.Parents[parent]; ok {
			return v
		}
	}
	return update
}

// applyTokenId returns the token ID to reuse when replaying STORETOKEN, or 0.
func applyTokenId(ctx Context) uint32 {
	if s := getChangelogApplyState(ctx); s != nil {
		return s.TokenId
	}
	return 0
}

// applyMode returns the mode recorded by the source, so that umask, default ACL
// inheritance and sgid clearing do not diverge on the destination.
func applyMode(ctx Context, computed uint16) uint16 {
	if s := getChangelogApplyState(ctx); s != nil && s.Mode != nil {
		return *s.Mode
	}
	return computed
}

func (m *baseMeta) sessionID(ctx Context) uint64 {
	if s := getChangelogApplyState(ctx); s != nil {
		return s.Sid
	}
	return m.sid
}

func (m *baseMeta) isOpen(ctx Context, inode Ino) bool {
	if s := getChangelogApplyState(ctx); s != nil {
		return s.Opened[inode]
	}
	return m.of.IsOpen(inode)
}

// Apply applies a validated changelog entry to dst.
func Apply(ctx Context, dst Meta, e *ChangeEntry) (err error) {
	ctx = ctx.WithValue(changelogApplyStateKey{}, &changelogApplyState{Time: e.Time, Sid: e.Sid})
	var skipReason string
	defer func() {
		if err != nil {
			return
		}
		if skipReason != "" {
			logger.Infof("Skipped changelog: version=%d operation=%s reason=%s", e.Ver, e.Op, skipReason)
		} else {
			logger.Infof("Applied changelog: version=%d operation=%s", e.Ver, e.Op)
		}
	}()
	defer dst.getBase().doFlushStats()
	switch e.Op {
	case OpCreate:
		return applyCreate(ctx, dst, e)
	case OpLink:
		return applyLink(ctx, dst, e)
	case OpUnlink:
		return applyUnlink(ctx, dst, e)
	case OpUnlinkBatch:
		return applyUnlinkBatch(ctx, dst, e)
	case OpRmdir:
		return applyRmdir(ctx, dst, e)
	case OpMove:
		return applyMove(ctx, dst, e)
	case OpClone:
		return applyClone(ctx, dst, e)
	case OpCloneBatch:
		return applyCloneBatch(ctx, dst, e)
	case OpAttach:
		return applyAttach(ctx, dst, e)
	case OpCleanup:
		inode, err := e.Ino(0)
		if err != nil {
			return err
		}
		return changelogCall(e, dst.getBase().en.doCleanupDetachedNode(ctx, inode))
	case OpSetXattr:
		return applySetXattr(ctx, dst, e)
	case OpRemoveXattr:
		return applyRemoveXattr(ctx, dst, e)
	case OpSetAttr:
		return applySetAttr(ctx, dst, e)
	case OpTruncate:
		return applyTruncate(ctx, dst, e)
	case OpFallocate:
		return applyFallocate(ctx, dst, e)
	case OpCopyFileRange:
		return applyCopyFileRange(ctx, dst, e)
	case OpWrite:
		return applyWrite(ctx, dst, e)
	case OpCompactChunk:
		return applyCompactChunk(ctx, dst, e)
	case OpIncrCounter:
		name := e.Args[0]
		delta, err := e.Int64(1)
		if err != nil {
			return err
		}
		if name == usedSpace || name == totalInodes {
			// FIXME: the loaded baseline must account for usage not flushed before dump.
			skipReason = fmt.Sprintf("%s is updated by metadata operations", name)
			return nil
		}
		if _, err := dst.getBase().en.incrCounter(name, delta); err != nil {
			return fmt.Errorf("%s %s: %w", e.Op, name, err)
		}
		return nil
	case OpSetFacl:
		return applySetFacl(ctx, dst, e)
	case OpSetQuota:
		return applySetQuota(ctx, dst, e)
	case OpDelQuota:
		return applyDelQuota(ctx, dst, e)
	case OpRepairDir:
		return applyRepairDir(ctx, dst, e)
	case OpUpdateToken:
		return applyUpdateToken(ctx, dst, e)
	case OpStoreToken:
		return applyStoreToken(ctx, dst, e)
	case OpDeleteTokens:
		return applyDeleteTokens(ctx, dst, e)
	case OpAccess:
		inode, err := e.Ino(0)
		if err != nil {
			return err
		}
		_, err = dst.getBase().en.doTouchAtime(ctx, inode, &Attr{}, e.Time)
		return err
	case OpDelSustained:
		sid, err := e.Uint64(0)
		if err != nil {
			return err
		}
		inode, err := e.Ino(1)
		if err != nil {
			return err
		}
		m := dst.getBase()
		if err := m.en.doDeleteSustainedInode(ctx, sid, inode); err != nil {
			return err
		}
		m.Lock()
		delete(m.removedFiles, inode)
		m.Unlock()
		return nil

	case OpNewSession, OpCleanSession:
		skipReason = "source client sessions are not applied"
		return nil
	case OpFlock, OpSetlk:
		skipReason = "source client locks are not applied"
		return nil
	case OpInitDirStats, OpInitUserGroupQuota:
		skipReason = "statistics initialization is not applied"
		return nil
	case OpDelChunk, OpDeleteSlice, OpCleanupDelayedSlices, OpCleanupTrashSlices:
		skipReason = "background cleanup is not applied"
		return nil
	case OpSet:
		skipReason = "background job timestamps are not applied"
		return nil
	default:
		return fmt.Errorf("%s is validated but not implemented by apply", e.Op)
	}
}

func changelogCall(e *ChangeEntry, st syscall.Errno) error {
	if st != 0 {
		return fmt.Errorf("%s failed: %s", e.Op, st)
	}
	return nil
}

func changelogVerifyIno(e *ChangeEntry, index int, actual Ino) error {
	expected, err := e.ResultIno(index)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("%s returned inode %d, changelog has %d", e.Op, actual, expected)
	}
	return nil
}

func applyCreate(ctx Context, dst Meta, e *ChangeEntry) error {
	parent, err := e.Ino(0)
	if err != nil {
		return err
	}
	name := e.Args[1]
	uid, err := e.Uint32(2)
	if err != nil {
		return err
	}
	gid, err := e.Uint32(3)
	if err != nil {
		return err
	}
	typ, err := e.Uint8(4)
	if err != nil {
		return err
	}
	rdev, err := e.Uint32(10)
	if err != nil {
		return err
	}
	mode, err := e.Uint16(5)
	if err != nil {
		return err
	}
	cumask, err := e.Uint16(6)
	if err != nil {
		return err
	}
	path := e.Args[7]
	expected, err := e.ResultIno(0)
	if err != nil {
		return err
	}
	finalMode, err := e.Uint16(11)
	if err != nil {
		return err
	}
	var inode Ino
	state := getChangelogApplyState(ctx)
	state.Inode = expected
	state.Mode = &finalMode
	updateParent, err := e.Bool(9)
	if err != nil {
		return err
	}
	state.Parents = map[Ino]bool{parent: updateParent}
	ctx = &wrapContext{Context: ctx, pid: ctx.Pid(), uid: uid, gids: []uint32{gid}}
	ctx = ctx.WithValue(CtxKey("behavior"), e.Args[8])
	// FIXME: CREATE does not record the ACL IDs inherited from the parent, so
	// apply can allocate different ones.
	if err := changelogCall(e, dst.Mknod(ctx, parent, name, typ, mode, cumask, rdev, path, &inode, nil)); err != nil {
		return err
	}
	return changelogVerifyIno(e, 0, inode)
}

func applyLink(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	parent, err := e.Ino(1)
	if err != nil {
		return err
	}
	name := e.Args[2]
	updateParent, err := e.Bool(3)
	if err != nil {
		return err
	}
	getChangelogApplyState(ctx).Parents = map[Ino]bool{parent: updateParent}
	var attr Attr
	if err := changelogCall(e, dst.Link(ctx, inode, parent, name, &attr)); err != nil {
		return err
	}
	actual, err := e.ResultUint64(0)
	if err != nil {
		return err
	}
	if uint64(attr.Nlink) != actual {
		return fmt.Errorf("%s returned nlink %d, changelog has %d", e.Op, attr.Nlink, actual)
	}
	return nil
}

func applyUnlink(ctx Context, dst Meta, e *ChangeEntry) error {
	parent, err := e.Ino(0)
	if err != nil {
		return err
	}
	name := e.Args[1]
	trash, err := e.Uint64(2)
	if err != nil {
		return err
	}
	opened, err := e.Bool(3)
	if err != nil {
		return err
	}
	updateParent, err := e.Bool(4)
	if err != nil {
		return err
	}
	inode, err := e.ResultIno(0)
	if err != nil {
		return err
	}
	state := getChangelogApplyState(ctx)
	state.Trash = Ino(trash)
	state.Parents = map[Ino]bool{parent: updateParent}
	state.Opened = map[Ino]bool{inode: opened}
	return changelogCall(e, dst.Unlink(ctx, parent, name, trash == 0))
}

func applyRmdir(ctx Context, dst Meta, e *ChangeEntry) error {
	parent, err := e.Ino(0)
	if err != nil {
		return err
	}
	name := e.Args[1]
	trash, err := e.Uint64(2)
	if err != nil {
		return err
	}
	getChangelogApplyState(ctx).Trash = Ino(trash)
	// FIXME: RMDIR lacks the source's parent-update decision; KV retry-based
	// skipping can leave parent timestamps and nlink different after apply.
	return changelogCall(e, dst.Rmdir(ctx, parent, name, trash == 0))
}

func applyMove(ctx Context, dst Meta, e *ChangeEntry) error {
	parentSrc, err := e.Ino(0)
	if err != nil {
		return err
	}
	nameSrc := e.Args[1]
	parentDst, err := e.Ino(2)
	if err != nil {
		return err
	}
	nameDst := e.Args[3]
	flags, err := e.Uint32(4)
	if err != nil {
		return err
	}
	overwritten, err := e.Ino(5)
	if err != nil {
		return err
	}
	trash, err := e.Ino(6)
	if err != nil {
		return err
	}
	opened, err := e.Bool(7)
	if err != nil {
		return err
	}
	state := getChangelogApplyState(ctx)
	state.Trash = trash
	state.Opened = map[Ino]bool{overwritten: opened}
	var inode Ino
	// FIXME: MOVE lacks parent-update decisions; parent timestamps can diverge.
	if err := changelogCall(e, dst.Rename(ctx, parentSrc, nameSrc, parentDst, nameDst, flags, &inode, nil)); err != nil {
		return err
	}
	return changelogVerifyIno(e, 0, inode)
}

func applyClone(ctx Context, dst Meta, e *ChangeEntry) error {
	srcIno, err := e.Ino(0)
	if err != nil {
		return err
	}
	parent, err := e.Ino(1)
	if err != nil {
		return err
	}
	name := e.Args[2]
	ino, err := e.Ino(3)
	if err != nil {
		return err
	}
	cmode, err := e.Uint8(4)
	if err != nil {
		return err
	}
	cumask, err := e.Uint16(5)
	if err != nil {
		return err
	}
	top, err := e.Bool(6)
	if err != nil {
		return err
	}
	if err := changelogVerifyIno(e, 0, ino); err != nil {
		return err
	}
	if ctx, err = changelogOwnerContext(ctx, e, 7, 8); err != nil {
		return err
	}
	m := dst.getBase()
	var attr Attr
	if err := changelogCall(e, m.en.doCloneEntry(ctx, srcIno, parent, name, ino, &attr, cmode, cumask, top)); err != nil {
		return err
	}
	m.en.updateStats(align4K(attr.Length), 1)
	m.updateUserGroupStat(ctx, attr.Uid, attr.Gid, align4K(attr.Length), 1)
	if top && attr.Typ != TypeDirectory {
		// Clone accounts the new entry in its parent; directories do it after ATTACH.
		m.updateDirStat(ctx, parent, int64(attr.Length), align4K(attr.Length), 1)
		m.updateDirQuota(ctx, parent, align4K(attr.Length), 1)
	}
	return nil
}

func applyCloneBatch(ctx Context, dst Meta, e *ChangeEntry) error {
	dstParent, err := e.Ino(0)
	if err != nil {
		return err
	}
	cmode, err := e.Uint8(1)
	if err != nil {
		return err
	}
	cumask, err := e.Uint16(2)
	if err != nil {
		return err
	}
	entries := make([]*Entry, len(e.Result))
	inodes := make([]Ino, len(e.Result))
	for i := range e.Result {
		srcIno, err := e.Ino(5 + i*2)
		if err != nil {
			return err
		}
		name, err := e.Str(6 + i*2)
		if err != nil {
			return err
		}
		entries[i] = &Entry{Inode: srcIno, Name: []byte(name)}
		if inodes[i], err = e.ResultIno(i); err != nil {
			return err
		}
	}
	ctx, err = changelogOwnerContext(ctx, e, 3, 4)
	if err != nil {
		return err
	}
	state := getChangelogApplyState(ctx)
	state.Inodes = inodes
	m := dst.getBase()
	// The engines ignore srcParent; the changelog does not record it.
	if err := changelogCall(e, m.BatchClone(ctx, 0, dstParent, entries, cmode, cumask, nil)); err != nil {
		return err
	}
	// Entries whose source disappeared are skipped silently, so check each one.
	for i, entry := range entries {
		var inode Ino
		var attr Attr
		if st := dst.Lookup(ctx, dstParent, string(entry.Name), &inode, &attr, false); st != 0 {
			return fmt.Errorf("%s lookup %q: %s", e.Op, entry.Name, st)
		}
		if inode != inodes[i] {
			return fmt.Errorf("%s: %q resolved to inode %d, changelog has %d", e.Op, entry.Name, inode, inodes[i])
		}
	}
	return nil
}

func applyAttach(ctx Context, dst Meta, e *ChangeEntry) error {
	dstIno, err := e.Ino(0)
	if err != nil {
		return err
	}
	parent, err := e.Ino(1)
	if err != nil {
		return err
	}
	name := e.Args[2]
	var attr Attr
	if err := changelogCall(e, dst.GetAttr(ctx, dstIno, &attr)); err != nil {
		return err
	}
	// Clone accounts the whole cloned tree in the quotas of the destination parent.
	var sum Summary
	if err := changelogCall(e, dst.GetSummary(ctx, dstIno, &sum, true, false)); err != nil {
		return err
	}
	m := dst.getBase()
	if err := changelogCall(e, m.en.doAttachDirNode(ctx, parent, dstIno, name)); err != nil {
		return err
	}
	m.updateDirStat(ctx, parent, int64(attr.Length), align4K(attr.Length), 1)
	m.updateDirQuota(ctx, parent, int64(sum.Size), int64(sum.Dirs)+int64(sum.Files))
	return nil
}

// changelogOwnerContext replays an operation as the user recorded in the changelog.
func changelogOwnerContext(ctx Context, e *ChangeEntry, uidIndex, gidsIndex int) (Context, error) {
	uid, err := e.Uint32(uidIndex)
	if err != nil {
		return nil, err
	}
	gids, err := e.Gids(gidsIndex)
	if err != nil {
		return nil, err
	}
	if len(gids) == 0 {
		return nil, fmt.Errorf("%s: argument %d has no group", e.Op, gidsIndex)
	}
	return &wrapContext{Context: ctx, pid: ctx.Pid(), uid: uid, gids: gids}, nil
}

func applySetXattr(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	name := e.Args[1]
	value := e.Args[2]
	flags, err := e.Uint32(3)
	if err != nil {
		return err
	}
	return changelogCall(e, dst.SetXattr(ctx, inode, name, []byte(value), flags))
}

func applyRemoveXattr(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	name := e.Args[1]
	return changelogCall(e, dst.RemoveXattr(ctx, inode, name))
}

func applySetAttr(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	set, err := e.Uint16(1)
	if err != nil {
		return err
	}
	sgid, err := e.Uint8(2)
	if err != nil {
		return err
	}
	attr, err := e.AttrFields(3)
	if err != nil {
		return err
	}
	// FIXME: SETATTR records an ACL ID but not its rule; apply needs to preserve
	// the source's ACL mapping when allocating ACLs or changing modes.
	return changelogCall(e, dst.SetAttr(ctx, inode, set, sgid, attr))
}

func applyWrite(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	indx, err := e.Uint32(1)
	if err != nil {
		return err
	}
	off, err := e.Uint32(2)
	if err != nil {
		return err
	}
	sliceId, err := e.Uint64(3)
	if err != nil {
		return err
	}
	sliceLen, err := e.Uint32(4)
	if err != nil {
		return err
	}
	mtime, err := e.Int64(5)
	if err != nil {
		return err
	}
	mtimensec, err := e.Uint32(6)
	if err != nil {
		return err
	}
	slice := Slice{Id: sliceId, Size: sliceLen, Len: sliceLen}
	return changelogCall(e, dst.Write(ctx, inode, indx, off, slice, time.Unix(mtime, int64(mtimensec))))
}

func applyCompactChunk(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	indx, err := e.Uint32(1)
	if err != nil {
		return err
	}
	skipped, err := e.Int64(2)
	if err != nil {
		return err
	}
	nslices, err := e.Int64(3)
	if err != nil {
		return err
	}
	pos, err := e.Uint32(4)
	if err != nil {
		return err
	}
	id, err := e.Uint64(5)
	if err != nil {
		return err
	}
	size, err := e.Uint32(6)
	if err != nil {
		return err
	}
	return changelogCall(e, dst.getBase().applyCompactChunk(ctx, inode, indx, int(skipped), int(nslices), pos, id, size))
}

// The source has written the merged slice; apply only updates metadata.
func (m *baseMeta) applyCompactChunk(ctx Context, inode Ino, indx uint32, skipped, nslices int, pos uint32, id uint64, size uint32) syscall.Errno {
	if skipped < 0 || nslices <= 0 {
		return syscall.EINVAL
	}
	ss, st := m.en.doRead(ctx, inode, indx)
	if st != 0 {
		return st
	}
	n := skipped + nslices
	if n > len(ss) {
		return syscall.EINVAL
	}
	compacted := ss[skipped:n]
	var delayed []byte
	if m.toTrash(0) {
		delayed = make([]byte, 0, len(compacted)*12)
		for _, s := range compacted {
			if s.id > 0 {
				delayed = append(delayed, m.encodeDelayedSlice(s.id, s.size)...)
			}
		}
	}
	origin := make([]byte, 0, n*sliceBytes)
	for _, s := range ss[:n] {
		origin = append(origin, marshalSlice(s.pos, s.id, s.size, s.off, s.len)...)
	}
	if st = m.en.doCompactChunk(inode, indx, origin, compacted, skipped, pos, id, size, delayed); st == 0 {
		m.of.InvalidateChunk(inode, indx)
	}
	return st
}

func applyTruncate(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	// Skip the recorded old length.
	length, err := e.Uint64(2)
	if err != nil {
		return err
	}
	flags, err := e.Uint8(3)
	if err != nil {
		return err
	}
	var attr Attr
	return changelogCall(e, dst.Truncate(ctx, inode, flags, length, &attr, true))
}

func applyFallocate(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	off, err := e.Uint64(1)
	if err != nil {
		return err
	}
	size, err := e.Uint64(2)
	if err != nil {
		return err
	}
	mode, err := e.Uint8(3)
	if err != nil {
		return err
	}
	var length uint64
	return changelogCall(e, dst.Fallocate(ctx, inode, mode, off, size, &length))
}

func applyCopyFileRange(ctx Context, dst Meta, e *ChangeEntry) error {
	fin, err := e.Ino(0)
	if err != nil {
		return err
	}
	offIn, err := e.Uint64(1)
	if err != nil {
		return err
	}
	fout, err := e.Ino(2)
	if err != nil {
		return err
	}
	offOut, err := e.Uint64(3)
	if err != nil {
		return err
	}
	size, err := e.Uint64(4)
	if err != nil {
		return err
	}
	var copied, outLength uint64
	// COPYFILERANGE currently requires flags=0.
	if err := changelogCall(e, dst.CopyFileRange(ctx, fin, offIn, fout, offOut, size, 0, &copied, &outLength)); err != nil {
		return err
	}
	expected, err := e.ResultUint64(0)
	if err != nil {
		return err
	}
	if outLength != expected {
		return fmt.Errorf("%s returned length %d, changelog has %d", e.Op, outLength, expected)
	}
	return nil
}

func applyUnlinkBatch(ctx Context, dst Meta, e *ChangeEntry) error {
	parent, err := e.Ino(0)
	if err != nil {
		return err
	}
	names := e.BatchNames()
	trash, err := e.Uint64(len(e.Args) - 2)
	if err != nil {
		return err
	}
	updateParent, err := e.Bool(len(e.Args) - 1)
	if err != nil {
		return err
	}
	state := getChangelogApplyState(ctx)
	state.Trash = Ino(trash)
	state.Parents = map[Ino]bool{parent: updateParent}
	state.Opened = make(map[Ino]bool, len(names))
	entries := make([]*Entry, len(names))
	for i, name := range names {
		var inode Ino
		var attr Attr
		if st := dst.Lookup(ctx, parent, name, &inode, &attr, false); st != 0 {
			return fmt.Errorf("%s lookup %q: %s", e.Op, name, st)
		}
		expected, err := e.ResultUint64(2 * i)
		if err != nil {
			return err
		}
		if uint64(inode) != expected {
			return fmt.Errorf("%s: %q resolved to inode %d, changelog has %d", e.Op, name, inode, expected)
		}
		opened, err := e.ResultBool(2*i + 1)
		if err != nil {
			return err
		}
		state.Opened[inode] = opened
		entries[i] = &Entry{Inode: inode, Name: []byte(name)}
	}
	var count uint64
	return changelogCall(e, dst.BatchUnlink(ctx, parent, entries, &count, trash == 0))
}

func applySetFacl(ctx Context, dst Meta, e *ChangeEntry) error {
	ino, err := e.Ino(0)
	if err != nil {
		return err
	}
	aclType, err := e.Uint8(1)
	if err != nil {
		return err
	}
	encoded := []byte(e.Args[2])
	rule := &aclAPI.Rule{}
	rule.Decode(encoded)
	mode, err := e.Uint16(3)
	if err != nil {
		return err
	}
	getChangelogApplyState(ctx).Mode = &mode
	// FIXME: SETFACL lacks the assigned ACL ID, so apply can allocate a different one.
	return changelogCall(e, dst.SetFacl(ctx, ino, aclType, rule))
}

// applyRepairDir rebuilds a directory inode; nlink is recounted on the
// destination because the changelog does not record it.
func applyRepairDir(ctx Context, dst Meta, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	attr, err := e.AttrFields(1)
	if err != nil {
		return err
	}
	var current Attr
	if err := changelogCall(e, dst.GetAttr(ctx, inode, &current)); err != nil {
		return err
	}
	attr.Parent = current.Parent
	attr.DefaultACL = current.DefaultACL
	attr.Typ = TypeDirectory
	attr.Length = 4 << 10
	attr.Full = true
	return changelogCall(e, dst.getBase().en.doRepair(ctx, inode, attr, false))
}

func applySetQuota(ctx Context, dst Meta, e *ChangeEntry) error {
	qtype, err := e.Uint32(0)
	if err != nil {
		return err
	}
	key, err := e.Uint64(1)
	if err != nil {
		return err
	}
	maxSpace, err := e.Int64(2)
	if err != nil {
		return err
	}
	maxInodes, err := e.Int64(3)
	if err != nil {
		return err
	}
	usedSpace, err := e.Int64(4)
	if err != nil {
		return err
	}
	usedInodes, err := e.Int64(5)
	if err != nil {
		return err
	}
	m := dst.getBase()
	created, err := m.en.doSetQuota(ctx, qtype, key, &Quota{
		MaxSpace:   maxSpace,
		MaxInodes:  maxInodes,
		UsedSpace:  usedSpace,
		UsedInodes: usedInodes,
	})
	if err != nil {
		return err
	}
	if created && qtype == DirQuotaType && usedSpace < 0 && usedInodes < 0 {
		return m.calcDirQuotaUsage(ctx, Ino(key), fmt.Sprintf("inode %d", key), false)
	}
	return nil
}

func applyDelQuota(ctx Context, dst Meta, e *ChangeEntry) error {
	qtype, err := e.Uint32(0)
	if err != nil {
		return err
	}
	key, err := e.Uint64(1)
	if err != nil {
		return err
	}
	return dst.getBase().en.doDelQuota(ctx, qtype, key)
}

func applyStoreToken(ctx Context, dst Meta, e *ChangeEntry) error {
	id, err := e.Uint32(0)
	if err != nil {
		return err
	}
	if id == 0 {
		return fmt.Errorf("%s: token ID must not be zero", e.Op)
	}
	getChangelogApplyState(ctx).TokenId = id
	actual, st := dst.StoreToken(ctx, []byte(e.Args[1]))
	if err := changelogCall(e, st); err != nil {
		return err
	}
	if actual != id {
		return fmt.Errorf("%s stored token %d, changelog has %d", e.Op, actual, id)
	}
	return nil
}

func applyUpdateToken(ctx Context, dst Meta, e *ChangeEntry) error {
	id, err := e.Uint32(0)
	if err != nil {
		return err
	}
	token := []byte(e.Args[1])
	return changelogCall(e, dst.UpdateToken(ctx, id, token))
}

func applyDeleteTokens(ctx Context, dst Meta, e *ChangeEntry) error {
	ids := make([]uint32, len(e.Args))
	for i := range e.Args {
		id, err := e.Uint32(i)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	return changelogCall(e, dst.DeleteTokens(ctx, ids))
}
