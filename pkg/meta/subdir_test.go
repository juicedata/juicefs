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
	"path"
	"syscall"
	"testing"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the same operations with a volume root, a virtual mount root, and a
// real descendant inode. Backend reads keep assertions independent of translation.
func testSubdirOperations(t *testing.T, m Meta) {
	t.Run("SubdirOperations", func(t *testing.T) {
		ctx := Background()
		base := m.getBase()
		defer base.aclCache.Clear()
		originalFormat := m.GetFormat()
		format := originalFormat.Clone()
		format.EnableACL = true
		require.NoError(t, m.Init(format, false))
		defer func() { require.NoError(t, m.Init(&originalFormat, false)) }()
		require.NoError(t, m.NewSession(true))
		defer m.CloseSession()
		const file = "subdir-file"
		const dir = "subdir-dir"
		const link = "subdir-link"
		create := func(t *testing.T, parent Ino, name string) Ino {
			t.Helper()
			var inode Ino
			require.Zero(t, m.Mknod(ctx, parent, name, TypeFile, 0644, 0, 0, "", &inode, nil))
			return inode
		}
		mkdir := func(t *testing.T, parent Ino, name string) Ino {
			t.Helper()
			var inode Ino
			require.Zero(t, m.Mkdir(ctx, parent, name, 0755, 0, 0, &inode, nil))
			return inode
		}
		lookup := func(t *testing.T, parent Ino, name string, want Ino) {
			t.Helper()
			var inode Ino
			var attr Attr
			st := base.en.doLookup(ctx, parent, name, &inode, &attr)
			if want == 0 {
				assert.Equal(t, syscall.ENOENT, st, "entry %d/%s must be absent", parent, name)
			} else {
				assert.Zero(t, st, "entry %d/%s must exist", parent, name)
				assert.Equal(t, want, inode, "entry %d/%s", parent, name)
			}
		}
		cases := []struct {
			name string
			run  func(t *testing.T, parent, realParent, otherParent Ino)
		}{
			{"CreateMknodMkdir", func(t *testing.T, parent, realParent, otherParent Ino) {
				var inode Ino
				var attr Attr
				require.Zero(t, m.Create(ctx, parent, file, 0644, 0, syscall.O_EXCL, &inode, &attr))
				t.Cleanup(func() { assert.Zero(t, m.Close(ctx, inode)) })
				assert.Equal(t, realParent, attr.Parent)
				lookup(t, realParent, file, inode)
				lookup(t, otherParent, file, 0)
				for _, typ := range []uint8{TypeFIFO, TypeSocket, TypeBlockDev, TypeCharDev} {
					name := "subdir-" + typeToString(typ)
					var node Ino
					require.Zero(t, m.Mknod(ctx, parent, name, typ, 0600, 0, 123, "", &node, &attr))
					assert.Equal(t, realParent, attr.Parent)
					lookup(t, realParent, name, node)
					require.Zero(t, m.Unlink(ctx, parent, name, true))
					lookup(t, realParent, name, 0)
				}
				d := mkdir(t, parent, dir)
				child := create(t, d, file)
				lookup(t, realParent, dir, d)
				lookup(t, d, file, child)
			}},
			{"LookupReaddirResolve", func(t *testing.T, parent, realParent, otherParent Ino) {
				inode := create(t, parent, file)
				d := mkdir(t, parent, dir)
				var got Ino
				var attr Attr
				require.Zero(t, m.Lookup(ctx, parent, file, &got, &attr, true))
				assert.Equal(t, inode, got)
				assert.Equal(t, realParent, attr.Parent)
				require.Zero(t, m.Lookup(ctx, parent, ".", &got, &attr, false))
				assert.Equal(t, realParent, got)
				require.Zero(t, m.Lookup(ctx, RootInode, "..", &got, &attr, false))
				assert.Equal(t, base.root, got, ".. must stay at the mount root")
				if st := m.Resolve(ctx, parent, file, &got, &attr, false); st != syscall.ENOTSUP {
					require.Zero(t, st)
					assert.Equal(t, inode, got)
				}
				var entries []*Entry
				require.Zero(t, m.Readdir(ctx, parent, 1, &entries))
				listed := make(map[string]Ino)
				for _, e := range entries {
					listed[string(e.Name)] = e.Inode
				}
				assert.Equal(t, inode, listed[file])
				assert.Equal(t, d, listed[dir])
				h, st := m.NewDirHandler(ctx, parent, true, nil)
				require.Zero(t, st)
				defer h.Close()
				streamed := make(map[string]Ino)
				for offset := 0; ; {
					batch, st := h.List(ctx, offset)
					require.Zero(t, st)
					if len(batch) == 0 {
						break
					}
					for _, e := range batch {
						streamed[string(e.Name)] = e.Inode
					}
					offset += len(batch)
				}
				assert.Equal(t, listed, streamed)
				assert.Equal(t, map[Ino]int{realParent: 1}, m.GetParents(ctx, inode))
				prefix := "/"
				if parent != RootInode {
					prefix = "/child"
				}
				assert.Equal(t, []string{path.Join(prefix, file)}, m.GetPaths(ctx, inode))
			}},
			{"AttributesAccess", func(t *testing.T, parent, realParent, otherParent Ino) {
				inode := create(t, parent, file)
				for _, target := range []struct{ visible, real Ino }{{parent, realParent}, {inode, inode}} {
					var before, attr Attr
					require.Zero(t, base.en.doGetAttr(ctx, target.real, &before))
					t.Cleanup(func() { assert.Zero(t, m.SetAttr(ctx, target.visible, SetAttrMode, 0, &Attr{Mode: before.Mode})) })
					require.Zero(t, m.CheckSetAttr(ctx, target.visible, SetAttrMode, Attr{Mode: 0700}))
					require.Zero(t, m.SetAttr(ctx, target.visible, SetAttrMode, 0, &Attr{Mode: 0700}))
					require.Zero(t, m.GetAttr(ctx, target.visible, &attr))
					assert.Equal(t, uint16(0700), attr.Mode)
					require.Zero(t, base.en.doGetAttr(ctx, target.real, &attr))
					assert.Equal(t, uint16(0700), attr.Mode)
					assert.Equal(t, syscall.EACCES, m.Access(NewContext(1, 65534, []uint32{65534}), target.visible, MODE_MASK_R, nil))
				}
			}},
			{"Xattr", func(t *testing.T, parent, realParent, otherParent Ino) {
				inode := create(t, parent, file)
				for _, target := range []struct{ visible, real Ino }{{parent, realParent}, {inode, inode}} {
					const key = "user.subdir-test"
					require.Zero(t, m.SetXattr(ctx, target.visible, key, []byte("value"), XattrCreate))
					var value, names []byte
					require.Zero(t, m.GetXattr(ctx, target.real, key, &value))
					assert.Equal(t, []byte("value"), value)
					require.Zero(t, m.ListXattr(ctx, target.visible, &names))
					assert.Contains(t, string(names), key+"\x00")
					require.Zero(t, m.RemoveXattr(ctx, target.visible, key))
					assert.Equal(t, ENOATTR, m.GetXattr(ctx, target.real, key, &value))
				}
			}},
			{"ACL", func(t *testing.T, parent, realParent, otherParent Ino) {
				// Save the volume-root rule too, so a misdirected operation cannot contaminate other tests.
				var rootAttr Attr
				require.Zero(t, base.en.doGetAttr(ctx, RootInode, &rootAttr))
				oldRule := aclAPI.EmptyRule()
				if rootAttr.AccessACL != aclAPI.None {
					require.Zero(t, base.en.doGetFacl(ctx, RootInode, aclAPI.TypeAccess, rootAttr.AccessACL, oldRule))
				}
				t.Cleanup(func() {
					assert.Zero(t, base.en.doSetFacl(ctx, RootInode, aclAPI.TypeAccess, oldRule))
					var attr Attr
					assert.Zero(t, base.en.doSetAttr(ctx, RootInode, SetAttrMode, 0, &Attr{Mode: rootAttr.Mode}, &attr))
				})
				inode := create(t, parent, file)
				for _, target := range []struct{ visible, real Ino }{{inode, inode}, {parent, realParent}} {
					rule := &aclAPI.Rule{Owner: 7, Group: 5, Mask: 5, Other: 0, NamedUsers: aclAPI.Entries{{Id: 65534, Perm: 4}}}
					require.Zero(t, m.SetFacl(ctx, target.visible, aclAPI.TypeAccess, rule))
					var got aclAPI.Rule
					assert.Zero(t, base.en.doGetFacl(ctx, target.real, aclAPI.TypeAccess, aclAPI.None, &got))
					assert.True(t, rule.IsEqual(&got), "ACL must be stored on the real inode")
					readRule := rule.Dup()
					readRule.NamedUsers = aclAPI.Entries{{Id: 65534, Perm: 6}}
					require.Zero(t, base.en.doSetFacl(ctx, target.real, aclAPI.TypeAccess, readRule))
					require.Zero(t, m.GetFacl(ctx, target.visible, aclAPI.TypeAccess, &got))
					assert.True(t, readRule.IsEqual(&got), "ACL must be read from the real inode")
					require.Zero(t, m.SetFacl(ctx, target.visible, aclAPI.TypeAccess, aclAPI.EmptyRule()))
					assert.Equal(t, ENOATTR, base.en.doGetFacl(ctx, target.real, aclAPI.TypeAccess, aclAPI.None, &got))
				}
			}},
			{"ReadWriteTruncateFallocate", func(t *testing.T, parent, realParent, otherParent Ino) {
				var inode Ino
				require.Zero(t, m.Create(ctx, parent, file, 0644, 0, 0, &inode, nil))
				require.Zero(t, m.Close(ctx, inode))
				require.Zero(t, m.Open(ctx, inode, syscall.O_RDWR, &Attr{}))
				t.Cleanup(func() { assert.Zero(t, m.Close(ctx, inode)) })
				var id uint64
				require.Zero(t, m.NewSlice(ctx, &id))
				slice := Slice{Id: id, Size: 8192, Len: 8192}
				require.Zero(t, m.Write(ctx, inode, 0, 0, slice, time.Now()))
				require.Zero(t, m.InvalidateChunkCache(ctx, inode, 0))
				var slices []Slice
				require.Zero(t, m.Read(ctx, inode, 0, &slices))
				assert.Equal(t, []Slice{slice}, slices)
				var attr Attr
				require.Zero(t, m.Truncate(ctx, inode, 0, 4097, &attr, false))
				assert.Equal(t, uint64(4097), attr.Length)
				var length uint64
				require.Zero(t, m.Fallocate(ctx, inode, 0, 8192, 4096, &length))
				assert.Equal(t, uint64(12288), length)
				require.Zero(t, base.en.doGetAttr(ctx, inode, &attr))
				assert.Equal(t, realParent, attr.Parent)
				assert.Equal(t, length, attr.Length)
			}},
			{"SymlinkLinkUnlink", func(t *testing.T, parent, realParent, otherParent Ino) {
				inode := create(t, parent, file)
				require.Zero(t, m.Link(ctx, inode, parent, link, nil))
				lookup(t, realParent, link, inode)
				assert.Equal(t, map[Ino]int{realParent: 2}, m.GetParents(ctx, inode))
				var symlink Ino
				require.Zero(t, m.Symlink(ctx, parent, dir, file, &symlink, nil))
				var target []byte
				require.Zero(t, m.ReadLink(ctx, symlink, &target))
				assert.Equal(t, file, string(target))
				for _, name := range []string{file, dir} {
					require.Zero(t, m.Unlink(ctx, parent, name, true))
					lookup(t, realParent, name, 0)
				}
				lookup(t, realParent, link, inode)
			}},
			{"Rename", func(t *testing.T, parent, realParent, otherParent Ino) {
				inode := create(t, parent, file)
				d := mkdir(t, parent, dir)
				require.Zero(t, m.Rename(ctx, parent, file, d, file, RenameNoReplace, nil, nil))
				lookup(t, realParent, file, 0)
				lookup(t, d, file, inode)
				require.Zero(t, m.Rename(ctx, d, file, parent, link, 0, nil, nil))
				lookup(t, realParent, link, inode)
				child := mkdir(t, d, "nested")
				require.Zero(t, m.Rename(ctx, d, "nested", parent, file, 0, nil, nil))
				lookup(t, realParent, file, child)
				var attr Attr
				require.Zero(t, base.en.doGetAttr(ctx, child, &attr))
				assert.Equal(t, realParent, attr.Parent)
				created := create(t, child, file)
				lookup(t, child, file, created)
			}},
			{"RenameExchange", func(t *testing.T, parent, realParent, otherParent Ino) {
				a := mkdir(t, parent, file)
				d := mkdir(t, parent, dir)
				b := mkdir(t, d, link)
				require.Zero(t, m.Rename(ctx, parent, file, d, link, RenameExchange, nil, nil))
				lookup(t, realParent, file, b)
				lookup(t, d, link, a)
				for _, entry := range []struct{ inode, parent Ino }{{a, d}, {b, realParent}} {
					var attr Attr
					require.Zero(t, base.en.doGetAttr(ctx, entry.inode, &attr))
					assert.Equal(t, entry.parent, attr.Parent)
					create(t, entry.inode, file)
				}
			}},
			{"BatchUnlink", func(t *testing.T, parent, realParent, otherParent Ino) {
				var entries []*Entry
				for _, name := range []string{file, dir, link} {
					inode := create(t, parent, name)
					entries = append(entries, &Entry{Inode: inode, Name: []byte(name), Attr: &Attr{Typ: TypeFile}})
				}
				outside := otherParent
				if outside == RootInode {
					outside = 0 // address the real volume root, not the mount root
				}
				distinct := create(t, outside, dir)
				require.Zero(t, m.Link(ctx, entries[2].Inode, outside, link, nil))
				base.doFlushDirStat()
				before, st := m.GetDirStat(ctx, realParent)
				require.Zero(t, st)
				require.Zero(t, m.BatchUnlink(ctx, parent, entries, nil, true))
				base.doFlushDirStat()
				after, st := m.GetDirStat(ctx, realParent)
				require.Zero(t, st)
				assert.Equal(t, before.inodes-3, after.inodes)
				assert.Equal(t, before.space-3*align4K(0), after.space)
				for _, e := range entries {
					lookup(t, realParent, string(e.Name), 0)
				}
				lookup(t, otherParent, dir, distinct)
				lookup(t, otherParent, link, entries[2].Inode)
			}},
			{"RmdirRemove", func(t *testing.T, parent, realParent, otherParent Ino) {
				d := mkdir(t, parent, dir)
				create(t, d, file)
				assert.Equal(t, syscall.ENOTEMPTY, m.Rmdir(ctx, parent, dir, true))
				require.Zero(t, m.Unlink(ctx, d, file, true))
				require.Zero(t, m.Rmdir(ctx, parent, dir, true))
				lookup(t, realParent, dir, 0)
				d = mkdir(t, parent, dir)
				child := mkdir(t, d, "nested")
				create(t, d, file)
				create(t, child, file)
				require.Zero(t, m.Remove(ctx, parent, dir, true, 2, nil))
				lookup(t, realParent, dir, 0)
			}},
			{"CopyFileRangeClone", func(t *testing.T, parent, realParent, otherParent Ino) {
				src := create(t, parent, file)
				dst := create(t, parent, link)
				var id uint64
				require.Zero(t, m.NewSlice(ctx, &id))
				slice := Slice{Id: id, Size: 8192, Len: 8192}
				require.Zero(t, m.Write(ctx, src, 0, 0, slice, time.Now()))
				var copied, length uint64
				require.Zero(t, m.CopyFileRange(ctx, src, 0, dst, 0, 8192, 0, &copied, &length))
				assert.Equal(t, uint64(8192), copied)
				assert.Equal(t, copied, length)
				var slices []Slice
				require.Zero(t, m.Read(ctx, dst, 0, &slices))
				assert.Equal(t, []Slice{slice}, slices)
				var count, total uint64
				require.Zero(t, m.Clone(ctx, parent, src, parent, dir, 0, 0, 1, &count, &total))
				assert.Equal(t, uint64(1), count)
				assert.Equal(t, count, total)
				var clone Ino
				var attr Attr
				require.Zero(t, base.en.doLookup(ctx, realParent, dir, &clone, &attr))
				assert.NotEqual(t, src, clone)
				assert.Equal(t, realParent, attr.Parent)
				require.Zero(t, m.Read(ctx, clone, 0, &slices))
				assert.Equal(t, []Slice{slice}, slices)
			}},
			{"DirectoryClone", func(t *testing.T, parent, realParent, otherParent Ino) {
				src := mkdir(t, parent, dir)
				create(t, src, file)
				base.doFlushDirStat()
				var count, total uint64
				require.Zero(t, m.Clone(ctx, parent, src, parent, link, 0, 0, 1, &count, &total))
				assert.Equal(t, uint64(2), count)
				assert.Equal(t, count, total)
				var clone, child Ino
				var attr Attr
				require.Zero(t, base.en.doLookup(ctx, realParent, link, &clone, &attr))
				assert.NotEqual(t, src, clone)
				require.Zero(t, base.en.doLookup(ctx, clone, file, &child, &attr))
				assert.Equal(t, uint8(TypeFile), attr.Typ)
			}},
			{"SummaryQuotaStatFS", func(t *testing.T, parent, realParent, otherParent Ino) {
				var before, after Summary
				require.Zero(t, m.GetSummary(ctx, parent, &before, true, true))
				qpath := "/"
				if realParent != RootInode {
					qpath = "/subdir-operations"
					if parent != RootInode {
						qpath += "/child"
					}
				}
				quota := &Quota{MaxInodes: int64(before.Files+before.Dirs) + 1, MaxSpace: int64(before.Size) + 4096}
				require.NoError(t, m.HandleQuota(ctx, QuotaSet, qpath, DirQuotaType, map[string]*Quota{qpath: quota}, true, false, false))
				base.loadQuotas()
				t.Cleanup(func() {
					assert.NoError(t, base.en.doDelQuota(ctx, DirQuotaType, uint64(realParent)))
					base.loadQuotas()
				})
				d := mkdir(t, parent, dir)
				create(t, d, file)
				base.doFlushDirStat()
				base.doFlushQuotas()
				q, err := base.en.doGetQuota(ctx, DirQuotaType, uint64(realParent))
				require.NoError(t, err)
				require.NotNil(t, q)
				assert.Equal(t, quota.MaxInodes, q.UsedInodes)
				assert.Equal(t, quota.MaxSpace, q.UsedSpace)
				assert.Equal(t, syscall.EDQUOT, m.Create(ctx, d, link, 0644, 0, 0, nil, nil))
				var total, avail, used, free uint64
				require.Zero(t, m.StatFS(ctx, parent, &total, &avail, &used, &free))
				assert.Equal(t, uint64(quota.MaxSpace), total)
				assert.Zero(t, avail)
				assert.Equal(t, uint64(quota.MaxInodes), used)
				assert.Zero(t, free)
				require.Zero(t, m.GetSummary(ctx, parent, &after, true, true))
				assert.Equal(t, before.Files+1, after.Files)
				assert.Equal(t, before.Dirs+1, after.Dirs)
				assert.Equal(t, before.Size+8192, after.Size)
				tree := &TreeSummary{Inode: parent, Path: "/", Type: TypeDirectory}
				require.Zero(t, m.GetTreeSummary(ctx, tree, 1, 10, true, SortBySize, nil))
				assert.Equal(t, after.Size, tree.Size)
			}},
			{"Locks", func(t *testing.T, parent, realParent, otherParent Ino) {
				inode := create(t, parent, file)
				require.Zero(t, m.Flock(ctx, inode, 1, syscall.F_WRLCK, false))
				assert.Equal(t, syscall.EAGAIN, m.Flock(ctx, inode, 2, syscall.F_WRLCK, false))
				plocks, flocks, err := m.ListLocks(ctx, inode)
				require.NoError(t, err)
				assert.Empty(t, plocks)
				assert.Len(t, flocks, 1)
				require.Zero(t, m.Flock(ctx, inode, 1, syscall.F_UNLCK, false))
				require.Zero(t, m.Setlk(ctx, inode, 1, false, syscall.F_WRLCK, 0, 4095, 123))
				ltype, start, end, pid := uint32(syscall.F_WRLCK), uint64(0), uint64(4095), uint32(0)
				require.Zero(t, m.Getlk(ctx, inode, 2, &ltype, &start, &end, &pid))
				assert.Equal(t, uint32(syscall.F_WRLCK), ltype)
				assert.Equal(t, uint32(123), pid)
				require.Zero(t, m.Setlk(ctx, inode, 1, false, syscall.F_UNLCK, 0, 4095, 123))
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, location := range []string{"volume-root", "subdir-root", "subdir-child"} {
					t.Run(location, func(t *testing.T) {
						root := base.root
						name := "subdir-operations"
						mount := mkdir(t, RootInode, name)
						t.Cleanup(func() {
							base.chroot(root)
							for _, n := range []string{file, dir, link, "subdir-fifo", "subdir-socket", "subdir-blockdev", "subdir-chardev"} {
								st := m.Remove(ctx, RootInode, n, true, 1, nil)
								assert.True(t, st == 0 || st == syscall.ENOENT, "cleanup %s: %s", n, st)
							}
							assert.Zero(t, m.Remove(ctx, RootInode, name, true, 1, nil))
						})
						parent, realParent, otherParent := RootInode, RootInode, mount
						if location != "volume-root" {
							realParent, otherParent = mount, RootInode
							if location == "subdir-child" {
								realParent = mkdir(t, mount, "child")
								parent = realParent
							}
							require.Zero(t, m.Chroot(ctx, name))
						}
						tc.run(t, parent, realParent, otherParent)
					})
				}
			})
		}
	})
}
