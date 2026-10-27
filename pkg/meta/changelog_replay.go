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
	"reflect"
	"syscall"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
)

type replayState struct {
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

// Replayer exclusively owns a metadata client for sequential changelog replay.
type Replayer struct {
	src    Meta
	dst    Meta
	aclIDs map[uint32]uint32
}

func NewReplayer(uri string, src Meta, dryRun bool) (*Replayer, error) {
	conf := DefaultConf()
	conf.NoCompact = true
	conf.NoBGJob = true
	conf.MaxDeletes = 0
	conf.ReadOnly = dryRun
	dst := NewClient(uri, conf)
	if _, err := dst.Load(true); err != nil {
		return nil, err
	}
	r := &Replayer{src: src, dst: dst, aclIDs: map[uint32]uint32{aclAPI.None: aclAPI.None}}
	srcBase, dstBase := src.getBase(), dst.getBase()
	if err := r.Validate(srcBase.getFormat()); err != nil {
		return nil, err
	}
	if err := srcBase.en.cacheACLs(Background()); err != nil {
		return nil, fmt.Errorf("load source ACLs: %w", err)
	}
	if dryRun {
		if err := dstBase.en.cacheACLs(Background()); err != nil {
			return nil, fmt.Errorf("load destination ACLs: %w", err)
		}
	} else {
		if err := dst.NewSession(false); err != nil {
			_ = dst.CloseSession()
			return nil, fmt.Errorf("initialize destination metadata client: %w", err)
		}
	}
	for id, rule := range srcBase.aclCache.GetAll() {
		if dstID := dstBase.aclCache.GetId(rule); dstID != aclAPI.None {
			r.aclIDs[id] = dstID
		}
	}
	return r, nil
}

// Validate requires the destination to be a copy of the source.
func (r *Replayer) Validate(src *Format) error {
	s, d := *src, *r.dst.getBase().getFormat()
	for _, f := range []*Format{&s, &d} {
		f.SecretKey, f.SessionToken = "", ""
		if err := f.Decrypt(); err != nil {
			return fmt.Errorf("decrypt volume encryption key: %w", err)
		}
		// Fields that may differ between a volume and its copy.
		f.AccessKey = ""
		f.UploadLimit, f.DownloadLimit = 0, 0
		f.MinClientVersion, f.MaxClientVersion = "", ""
		f.ChangeLog, f.ChangeLogMaxAge, f.ChangeLogMaxLines = false, 0, 0
	}
	if !reflect.DeepEqual(s, d) {
		return fmt.Errorf("destination is not a copy of the source:\nsource: %s\ndestination: %s", &s, &d)
	}
	return nil
}

func (r *Replayer) Close() error {
	if r.dst.getBase().conf.ReadOnly {
		return nil
	}
	return r.dst.CloseSession()
}

func (r *Replayer) mapACL(ctx Context, id uint32) (uint32, error) {
	if dstID, ok := r.aclIDs[id]; ok {
		return dstID, nil
	}
	src, dst := r.src.getBase(), r.dst.getBase()
	rule := src.aclCache.Get(id)
	if rule == nil {
		rule = &aclAPI.Rule{}
		if st := src.en.doGetFacl(ctx, RootInode, aclAPI.TypeAccess, id, rule); st != 0 {
			return 0, fmt.Errorf("read source ACL %d: %w", id, st)
		}
	}
	dstID := dst.aclCache.GetId(rule)
	if dstID == aclAPI.None {
		var err error
		dstID, err = dst.en.doInsertACL(ctx, rule)
		if err != nil {
			return 0, fmt.Errorf("map source ACL %d: %w", id, err)
		}
	}
	r.aclIDs[id] = dstID
	return dstID, nil
}

// These helpers supply client-side inputs to metadata operations, using the
// recorded source values during replay.

func (m *baseMeta) now() time.Time {
	if s := m.replay.Load(); s != nil {
		return s.Time
	}
	return time.Now()
}

func (m *baseMeta) shouldUpdateParent(parent Ino, update bool) bool {
	if s := m.replay.Load(); s != nil {
		if v, ok := s.Parents[parent]; ok {
			return v
		}
	}
	return update
}

// tokenID returns the token ID to reuse, or 0 to allocate a new one.
func (m *baseMeta) tokenID() uint32 {
	if s := m.replay.Load(); s != nil {
		return s.TokenId
	}
	return 0
}

func (m *baseMeta) finalMode(computed uint16) uint16 {
	if s := m.replay.Load(); s != nil && s.Mode != nil {
		return *s.Mode
	}
	return computed
}

func (m *baseMeta) sessionID() uint64 {
	if s := m.replay.Load(); s != nil {
		return s.Sid
	}
	return m.sid
}

func (m *baseMeta) isOpen(inode Ino) bool {
	if s := m.replay.Load(); s != nil {
		return s.Opened[inode]
	}
	return m.of.IsOpen(inode)
}

func (m *baseMeta) checkPermission(ctx Context) bool {
	return m.replay.Load() == nil && ctx.CheckPermission()
}

// Replay replays a validated changelog entry. Calls must be serialized.
func (r *Replayer) Replay(ctx Context, e *ChangeEntry) (err error) {
	dst := r.dst.getBase()
	dst.replay.Store(&replayState{Time: e.Time, Sid: e.Sid})
	defer dst.replay.Store(nil)
	var skipReason string
	defer func() {
		if err != nil {
			return
		}
		if skipReason != "" {
			logger.Infof("Skipped changelog: version=%d operation=%s reason=%s", e.Ver, e.Op, skipReason)
		} else {
			logger.Infof("Replayed changelog: version=%d operation=%s", e.Ver, e.Op)
		}
	}()
	defer dst.doFlushStats()
	switch e.Op {
	case OpCreate:
		return r.create(ctx, e)
	case OpLink:
		return r.link(ctx, e)
	case OpUnlink:
		return r.unlink(ctx, e)
	case OpUnlinkBatch:
		return r.unlinkBatch(ctx, e)
	case OpRmdir:
		return r.rmdir(ctx, e)
	case OpMove:
		return r.move(ctx, e)
	case OpClone:
		return r.clone(ctx, e)
	case OpCloneBatch:
		return r.cloneBatch(ctx, e)
	case OpAttach:
		return r.attach(ctx, e)
	case OpCleanup:
		inode, err := e.Ino(0)
		if err != nil {
			return err
		}
		return changelogCall(e, dst.en.doCleanupDetachedNode(ctx, inode))
	case OpSetXattr:
		return r.setXattr(ctx, e)
	case OpRemoveXattr:
		return r.removeXattr(ctx, e)
	case OpSetAttr:
		return r.setAttr(ctx, e)
	case OpTruncate:
		return r.truncate(ctx, e)
	case OpFallocate:
		return r.fallocate(ctx, e)
	case OpCopyFileRange:
		return r.copyFileRange(ctx, e)
	case OpWrite:
		return r.write(ctx, e)
	case OpCompactChunk:
		return r.compactChunk(ctx, e)
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
		if name == aclCounter {
			skipReason = "ACL IDs are allocated by the destination"
			return nil
		}
		if _, err := dst.en.incrCounter(name, delta); err != nil {
			return fmt.Errorf("%s %s: %w", e.Op, name, err)
		}
		return nil
	case OpSetFacl:
		return r.setFacl(ctx, e)
	case OpSetQuota:
		return r.setQuota(ctx, e)
	case OpDelQuota:
		return r.delQuota(ctx, e)
	case OpRepairDir:
		return r.repairDir(ctx, e)
	case OpUpdateToken:
		return r.updateToken(ctx, e)
	case OpStoreToken:
		return r.storeToken(ctx, e)
	case OpDeleteTokens:
		return r.deleteTokens(ctx, e)
	case OpAccess:
		inode, err := e.Ino(0)
		if err != nil {
			return err
		}
		_, err = dst.en.doTouchAtime(ctx, inode, &Attr{}, e.Time)
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
		if err := dst.en.doDeleteSustainedInode(ctx, sid, inode); err != nil {
			return err
		}
		dst.Lock()
		delete(dst.removedFiles, inode)
		dst.Unlock()
		return nil

	case OpNewSession, OpCleanSession:
		skipReason = "source client sessions are not replayed"
		return nil
	case OpFlock, OpSetlk:
		skipReason = "source client locks are not replayed"
		return nil
	case OpInitDirStats, OpInitUserGroupQuota:
		skipReason = "statistics initialization is not replayed"
		return nil
	case OpDelChunk, OpDeleteSlice, OpCleanupDelayedSlices, OpCleanupTrashSlices:
		skipReason = "background cleanup is not replayed"
		return nil
	case OpSet:
		skipReason = "background job timestamps are not replayed"
		return nil
	default:
		return fmt.Errorf("%s is validated but not implemented by replay", e.Op)
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

func (r *Replayer) create(ctx Context, e *ChangeEntry) error {
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
	srcMode, err := e.Uint16(11)
	if err != nil {
		return err
	}
	var inode Ino
	state := r.dst.getBase().replay.Load()
	state.Inode = expected
	state.Mode = &srcMode
	updateParent, err := e.Bool(9)
	if err != nil {
		return err
	}
	state.Parents = map[Ino]bool{parent: updateParent}
	ctx = WrapWithoutCancel(ctx, ctx.Pid(), uid, []uint32{gid})
	ctx = ctx.WithValue(CtxKey("behavior"), e.Args[8])
	if err := changelogCall(e, r.dst.Mknod(ctx, parent, name, typ, mode, cumask, rdev, path, &inode, nil)); err != nil {
		return err
	}
	return changelogVerifyIno(e, 0, inode)
}

func (r *Replayer) link(ctx Context, e *ChangeEntry) error {
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
	r.dst.getBase().replay.Load().Parents = map[Ino]bool{parent: updateParent}
	var attr Attr
	if err := changelogCall(e, r.dst.Link(ctx, inode, parent, name, &attr)); err != nil {
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

func (r *Replayer) unlink(ctx Context, e *ChangeEntry) error {
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
	state := r.dst.getBase().replay.Load()
	state.Trash = Ino(trash)
	state.Parents = map[Ino]bool{parent: updateParent}
	state.Opened = map[Ino]bool{inode: opened}
	return changelogCall(e, r.dst.Unlink(ctx, parent, name, trash == 0))
}

func (r *Replayer) rmdir(ctx Context, e *ChangeEntry) error {
	parent, err := e.Ino(0)
	if err != nil {
		return err
	}
	name := e.Args[1]
	trash, err := e.Uint64(2)
	if err != nil {
		return err
	}
	r.dst.getBase().replay.Load().Trash = Ino(trash)
	// FIXME: RMDIR lacks the source's parent-update decision; KV retry-based
	// skipping can leave parent timestamps and nlink different after replay.
	return changelogCall(e, r.dst.Rmdir(ctx, parent, name, trash == 0))
}

func (r *Replayer) move(ctx Context, e *ChangeEntry) error {
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
	updateSrc, err := e.Bool(8)
	if err != nil {
		return err
	}
	updateDst, err := e.Bool(9)
	if err != nil {
		return err
	}
	state := r.dst.getBase().replay.Load()
	state.Trash = trash
	state.Opened = map[Ino]bool{overwritten: opened}
	state.Parents = map[Ino]bool{parentSrc: updateSrc, parentDst: updateDst}
	var inode Ino
	if err := changelogCall(e, r.dst.Rename(ctx, parentSrc, nameSrc, parentDst, nameDst, flags, &inode, nil)); err != nil {
		return err
	}
	return changelogVerifyIno(e, 0, inode)
}

func (r *Replayer) clone(ctx Context, e *ChangeEntry) error {
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
	m := r.dst.getBase()
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

func (r *Replayer) cloneBatch(ctx Context, e *ChangeEntry) error {
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
	m := r.dst.getBase()
	state := m.replay.Load()
	state.Inodes = inodes
	// The engines ignore srcParent; the changelog does not record it.
	if err := changelogCall(e, m.BatchClone(ctx, 0, dstParent, entries, cmode, cumask, nil)); err != nil {
		return err
	}
	// Entries whose source disappeared are skipped silently, so check each one.
	for i, entry := range entries {
		var inode Ino
		var attr Attr
		if st := r.dst.Lookup(ctx, dstParent, string(entry.Name), &inode, &attr, false); st != 0 {
			return fmt.Errorf("%s lookup %q: %s", e.Op, entry.Name, st)
		}
		if inode != inodes[i] {
			return fmt.Errorf("%s: %q resolved to inode %d, changelog has %d", e.Op, entry.Name, inode, inodes[i])
		}
	}
	return nil
}

func (r *Replayer) attach(ctx Context, e *ChangeEntry) error {
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
	if err := changelogCall(e, r.dst.GetAttr(ctx, dstIno, &attr)); err != nil {
		return err
	}
	// Clone accounts the whole cloned tree in the quotas of the destination parent.
	var sum Summary
	if err := changelogCall(e, r.dst.GetSummary(ctx, dstIno, &sum, true, false)); err != nil {
		return err
	}
	m := r.dst.getBase()
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
	return WrapWithoutCancel(ctx, ctx.Pid(), uid, gids), nil
}

func (r *Replayer) setXattr(ctx Context, e *ChangeEntry) error {
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
	return changelogCall(e, r.dst.SetXattr(ctx, inode, name, []byte(value), flags))
}

func (r *Replayer) removeXattr(ctx Context, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	name := e.Args[1]
	return changelogCall(e, r.dst.RemoveXattr(ctx, inode, name))
}

func (r *Replayer) setAttr(ctx Context, e *ChangeEntry) error {
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
	if set&(SetAttrMode|SetAttrUID|SetAttrGID) != 0 {
		mode := attr.Mode
		r.dst.getBase().replay.Load().Mode = &mode
	}
	return changelogCall(e, r.dst.SetAttr(ctx, inode, set, sgid, attr))
}

func (r *Replayer) write(ctx Context, e *ChangeEntry) error {
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
	return changelogCall(e, r.dst.Write(ctx, inode, indx, off, slice, time.Unix(mtime, int64(mtimensec))))
}

func (r *Replayer) compactChunk(ctx Context, e *ChangeEntry) error {
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
	return changelogCall(e, r.applyCompactChunk(ctx, inode, indx, int(skipped), int(nslices), pos, id, size))
}

// The source has written the merged slice; replay only updates metadata.
func (r *Replayer) applyCompactChunk(ctx Context, inode Ino, indx uint32, skipped, nslices int, pos uint32, id uint64, size uint32) syscall.Errno {
	m := r.dst.getBase()
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

func (r *Replayer) truncate(ctx Context, e *ChangeEntry) error {
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
	return changelogCall(e, r.dst.Truncate(ctx, inode, flags, length, &attr, true))
}

func (r *Replayer) fallocate(ctx Context, e *ChangeEntry) error {
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
	return changelogCall(e, r.dst.Fallocate(ctx, inode, mode, off, size, &length))
}

func (r *Replayer) copyFileRange(ctx Context, e *ChangeEntry) error {
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
	if err := changelogCall(e, r.dst.CopyFileRange(ctx, fin, offIn, fout, offOut, size, 0, &copied, &outLength)); err != nil {
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

func (r *Replayer) unlinkBatch(ctx Context, e *ChangeEntry) error {
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
	state := r.dst.getBase().replay.Load()
	state.Trash = Ino(trash)
	state.Parents = map[Ino]bool{parent: updateParent}
	state.Opened = make(map[Ino]bool, len(names))
	entries := make([]*Entry, len(names))
	for i, name := range names {
		var inode Ino
		var attr Attr
		if st := r.dst.Lookup(ctx, parent, name, &inode, &attr, false); st != 0 {
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
	return changelogCall(e, r.dst.BatchUnlink(ctx, parent, entries, &count, trash == 0))
}

func (r *Replayer) setFacl(ctx Context, e *ChangeEntry) error {
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
	r.dst.getBase().replay.Load().Mode = &mode
	return changelogCall(e, r.dst.SetFacl(ctx, ino, aclType, rule))
}

// repairDir rebuilds a directory inode; nlink is recounted on the
// destination because the changelog does not record it.
func (r *Replayer) repairDir(ctx Context, e *ChangeEntry) error {
	inode, err := e.Ino(0)
	if err != nil {
		return err
	}
	attr, err := e.AttrFields(1)
	if err != nil {
		return err
	}
	var current Attr
	if err := changelogCall(e, r.dst.GetAttr(ctx, inode, &current)); err != nil {
		return err
	}
	attr.AccessACL, err = r.mapACL(ctx, attr.AccessACL)
	if err != nil {
		return err
	}
	attr.Parent = current.Parent
	attr.DefaultACL = current.DefaultACL
	attr.Typ = TypeDirectory
	attr.Length = 4 << 10
	attr.Full = true
	return changelogCall(e, r.dst.getBase().en.doRepair(ctx, inode, attr, false))
}

func (r *Replayer) setQuota(ctx Context, e *ChangeEntry) error {
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
	m := r.dst.getBase()
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

func (r *Replayer) delQuota(ctx Context, e *ChangeEntry) error {
	qtype, err := e.Uint32(0)
	if err != nil {
		return err
	}
	key, err := e.Uint64(1)
	if err != nil {
		return err
	}
	return r.dst.getBase().en.doDelQuota(ctx, qtype, key)
}

func (r *Replayer) storeToken(ctx Context, e *ChangeEntry) error {
	id, err := e.Uint32(0)
	if err != nil {
		return err
	}
	if id == 0 {
		return fmt.Errorf("%s: token ID must not be zero", e.Op)
	}
	r.dst.getBase().replay.Load().TokenId = id
	actual, st := r.dst.StoreToken(ctx, []byte(e.Args[1]))
	if err := changelogCall(e, st); err != nil {
		return err
	}
	if actual != id {
		return fmt.Errorf("%s stored token %d, changelog has %d", e.Op, actual, id)
	}
	return nil
}

func (r *Replayer) updateToken(ctx Context, e *ChangeEntry) error {
	id, err := e.Uint32(0)
	if err != nil {
		return err
	}
	token := []byte(e.Args[1])
	return changelogCall(e, r.dst.UpdateToken(ctx, id, token))
}

func (r *Replayer) deleteTokens(ctx Context, e *ChangeEntry) error {
	ids := make([]uint32, len(e.Args))
	for i := range e.Args {
		id, err := e.Uint32(i)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	return changelogCall(e, r.dst.DeleteTokens(ctx, ids))
}
