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
	"context"
	"testing"
)

// fuseDefaultCtx 模拟 FUSE 默认模式（NonDefaultPermission=false）下的 Context：
// Gids() 只返回主组，不返回附加组；CheckPermission() 返回 false。
// 这与 pkg/fuse/context.go 中 checkPermission=false 时的行为完全一致。
type fuseDefaultCtx struct {
	context.Context
	pid uint32
	uid uint32
	gid uint32 // 只有主组，附加组不可见
}

func (c *fuseDefaultCtx) Uid() uint32           { return c.uid }
func (c *fuseDefaultCtx) Gid() uint32           { return c.gid }
func (c *fuseDefaultCtx) Gids() []uint32        { return []uint32{c.gid} } // 只返回主组，模拟 FUSE 默认行为
func (c *fuseDefaultCtx) Pid() uint32           { return c.pid }
func (c *fuseDefaultCtx) Cancel()               {}
func (c *fuseDefaultCtx) Canceled() bool        { return false }
func (c *fuseDefaultCtx) CheckPermission() bool { return false } // NonDefaultPermission=false
func (c *fuseDefaultCtx) WithValue(k, v interface{}) Context {
	cp := *c
	cp.Context = context.WithValue(c.Context, k, v)
	return &cp
}

// newFuseDefaultCtx 创建模拟 FUSE 默认模式的 Context。
// supplementaryGids 会被故意丢弃，与 FUSE 默认行为一致。
func newFuseDefaultCtx(uid, primaryGid uint32) *fuseDefaultCtx {
	return &fuseDefaultCtx{
		Context: context.Background(),
		uid:     uid,
		gid:     primaryGid,
	}
}

// TestSgidSetBySecondaryGroup_MetaFix 验证 meta 层（containsGid 修复后）
// 在 Context.Gids() 能正确返回附加组时，setgid 位被正确保留。
// 对应 pkg/meta/base.go mergeAttr 中的修复。
func TestSgidSetBySecondaryGroup_MetaFix(t *testing.T) {
	m := setupTestMeta(t)

	// 用户 uid=10, 主组 gid=10, 附加组 gid=20
	// NewContext 来自 meta 包，Gids() 正确返回所有组 [10, 20]
	ctx := NewContext(1, 10, []uint32{10, 20})

	var dirIno Ino
	attr := &Attr{}

	// 1. 建目录（归属 gid=10）
	if st := m.Mkdir(ctx, RootInode, "testdir_fix", 0755, 0, 0, &dirIno, attr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	defer m.Rmdir(ctx, RootInode, "testdir_fix") //nolint:errcheck

	// 2. chgrp 到附加组 gid=20
	if st := m.SetAttr(ctx, dirIno, SetAttrGID, 0, &Attr{Gid: 20}); st != 0 {
		t.Fatalf("chgrp to secondary group: %s", st)
	}

	// 3. chmod g+s（设置 setgid 位）
	if st := m.SetAttr(ctx, dirIno, SetAttrMode, 0, &Attr{Mode: 02755}); st != 0 {
		t.Fatalf("chmod g+s: %s", st)
	}

	// 4. 验证 setgid 位是否被保留
	if st := m.GetAttr(ctx, dirIno, attr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}
	if attr.Mode&02000 == 0 {
		t.Fatalf("BUG: setgid bit was silently stripped! mode=0%o", attr.Mode)
	}
	t.Logf("PASS: setgid bit preserved, mode=0%o", attr.Mode)
}

// TestSgidSetBySecondaryGroup_FuseDefaultMode 复现 FUSE 默认模式下的 bug：
// 使用 fuseDefaultCtx（Gids() 只返回主组），模拟线上实际场景。
// 预期：setgid 位被错误地剥离（silent failure），这就是用户报告的 bug。
func TestSgidSetBySecondaryGroup_FuseDefaultMode(t *testing.T) {
	m := setupTestMeta(t)

	// fuseDefaultCtx 模拟 NonDefaultPermission=false 时的 FUSE context
	// 用户 uid=10, 主组 gid=10；附加组 gid=20 对 meta 层不可见
	ctx := newFuseDefaultCtx(10, 10)

	// 用 root context 预先创建目录，并将 gid 改成 20（模拟已 chgrp 到附加组）
	rootCtx := NewContext(0, 0, []uint32{0})
	var dirIno Ino
	attr := &Attr{}

	if st := m.Mkdir(rootCtx, RootInode, "testdir_fuse", 0755, 0, 0, &dirIno, attr); st != 0 {
		t.Fatalf("mkdir: %s", st)
	}
	defer m.Rmdir(rootCtx, RootInode, "testdir_fuse") //nolint:errcheck

	// root 将目录 owner 改成 uid=10，gid=20
	if st := m.SetAttr(rootCtx, dirIno, SetAttrUID|SetAttrGID, 0, &Attr{Uid: 10, Gid: 20}); st != 0 {
		t.Fatalf("chown: %s", st)
	}

	// 现在以普通用户（fuseDefaultCtx）执行 chmod g+s
	// 用户主组是 10，但目录的 gid 是 20（属于用户的附加组，但 FUSE 默认模式感知不到）
	if st := m.SetAttr(ctx, dirIno, SetAttrMode, 0, &Attr{Mode: 02755}); st != 0 {
		t.Fatalf("chmod g+s: %s", st)
	}

	if st := m.GetAttr(ctx, dirIno, attr); st != 0 {
		t.Fatalf("getattr: %s", st)
	}

	if attr.Mode&02000 == 0 {
		t.Fatalf("BUG: setgid bit was silently stripped! mode=0%o", attr.Mode)
	}
	t.Logf("PASS: setgid bit preserved (CheckPermission=false skips strip), mode=0%o", attr.Mode)
}

// setupTestMeta 创建一个 SQLite 文件后端的 meta 实例供测试使用。
func setupTestMeta(t *testing.T) Meta {
	t.Helper()
	m, err := newSQLMeta("sqlite3", t.TempDir()+"/sgid-test.db", testConfig())
	if err != nil {
		t.Fatalf("new meta client: %v", err)
	}
	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %v", err)
	}
	format := testFormat()
	format.Name = "test-sgid-repro"
	if err := m.Init(format, true); err != nil {
		t.Fatalf("init meta: %v", err)
	}
	return m
}
