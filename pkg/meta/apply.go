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

type applyInodeKey struct{}

type applyModeKey struct{}

// Replay must not trigger auto-compaction.
func withApplyMode(ctx Context) Context {
	return ctx.WithValue(applyModeKey{}, true)
}

func isApplyMode(ctx Context) bool {
	v, _ := ctx.Value(applyModeKey{}).(bool)
	return v
}

// Apply replays a validated changelog entry on dst.
func Apply(ctx Context, dst Meta, e *ChangeEntry) error {
	ctx = withApplyMode(ctx)
	switch e.Op {
	case OpCreate:
		return applyCreate(dst, e)
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
		return applyIncrCounter(dst, e)
	case OpSetFacl:
		return applySetFacl(ctx, dst, e)
	case OpSetQuota:
		return applySetQuota(ctx, dst, e)
	case OpDelQuota:
		return applyDelQuota(ctx, dst, e)
	case OpUpdateToken:
		return applyUpdateToken(ctx, dst, e)
	case OpDeleteTokens:
		return applyDeleteTokens(ctx, dst, e)

	case OpNewSession, OpCleanSession, OpDelSustained, OpAccess,
		OpInitDirStats, OpInitUserGroupQuota, OpDelChunk, OpDeleteSlice,
		OpCleanupDelayedSlices, OpCleanupTrashSlices, OpCleanup, OpSet:
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

func applyCreate(dst Meta, e *ChangeEntry) error {
	parent, err := e.Ino(0)
	if err != nil {
		return err
	}
	if parent.IsTrash() {
		// Deletes recreate trash directories on the destination.
		return nil
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
	var inode Ino
	ctx := withApplyMode(NewContext(uint32(e.Sid), uid, []uint32{gid})).WithValue(applyInodeKey{}, expected)
	if err := changelogCall(e, dst.Mknod(ctx, parent, name, typ, mode, cumask, 0, path, &inode, nil)); err != nil {
		return err
	}
	return changelogVerifyIno(e, 0, inode)
}

func applyIncrCounter(dst Meta, e *ChangeEntry) error {
	name := e.Args[0]
	delta, err := e.Int64(1)
	if err != nil {
		return err
	}
	if _, err := dst.getBase().en.incrCounter(name, delta); err != nil {
		return fmt.Errorf("%s %s: %w", e.Op, name, err)
	}
	return nil
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
	// Nonzero trash lets the destination choose its own trash directory.
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
	var inode Ino
	if err := changelogCall(e, dst.Rename(ctx, parentSrc, nameSrc, parentDst, nameDst, flags, &inode, nil)); err != nil {
		return err
	}
	return changelogVerifyIno(e, 0, inode)
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
	var values [11]uint64
	for i := range values {
		values[i], err = e.Uint64(3 + i)
		if err != nil {
			return err
		}
	}
	attr := &Attr{
		Uid:       uint32(values[0]),
		Gid:       uint32(values[1]),
		Mode:      uint16(values[2]),
		Flags:     uint8(values[3]),
		Atime:     int64(values[4]),
		Mtime:     int64(values[5]),
		Atimensec: uint32(values[6]),
		Mtimensec: uint32(values[7]),
		Ctime:     int64(values[8]),
		Ctimensec: uint32(values[9]),
		AccessACL: uint32(values[10]),
	}
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

// The source has written the merged slice; replay only updates metadata.
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
	entries := make([]*Entry, len(names))
	for i, name := range names {
		var inode Ino
		var attr Attr
		if st := dst.Lookup(ctx, parent, name, &inode, &attr, false); st != 0 {
			return fmt.Errorf("%s lookup %q: %s", e.Op, name, st)
		}
		expected, err := e.ResultUint64(i)
		if err != nil {
			return err
		}
		if uint64(inode) != expected {
			return fmt.Errorf("%s: %q resolved to inode %d, changelog has %d", e.Op, name, inode, expected)
		}
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
	return changelogCall(e, dst.SetFacl(ctx, ino, aclType, rule))
}

// HandleQuota expects a path for directories, or a decimal uid/gid.
func quotaKey(ctx Context, dst Meta, qtype uint32, key uint64) (string, error) {
	if qtype != DirQuotaType {
		return fmt.Sprintf("%d", key), nil
	}
	paths := dst.GetPaths(ctx, Ino(key))
	if len(paths) == 0 {
		return "", fmt.Errorf("no path found for directory inode %d", key)
	}
	return paths[0], nil
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
	qkey, err := quotaKey(ctx, dst, qtype, key)
	if err != nil {
		return err
	}
	quotas := map[string]*Quota{qkey: {MaxSpace: maxSpace, MaxInodes: maxInodes}}
	return dst.HandleQuota(ctx, QuotaSet, qkey, qtype, quotas, false, false, true)
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
	qkey, err := quotaKey(ctx, dst, qtype, key)
	if err != nil {
		return err
	}
	return dst.HandleQuota(ctx, QuotaDel, qkey, qtype, nil, false, false, false)
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
