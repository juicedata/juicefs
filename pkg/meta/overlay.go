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
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	aclAPI "github.com/juicedata/juicefs/pkg/acl"
)

const (
	whiteoutPrefix = ".wh."
	opaqueMarker   = ".wh..wh..opq"
)

func isWhiteoutName(name string) bool {
	return strings.HasPrefix(name, whiteoutPrefix)
}

func whiteoutName(name string) string {
	return whiteoutPrefix + name
}

func cleanRelDir(p string) string {
	p = strings.TrimSpace(p)
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

func dirsOverlap(a, b string) bool {
	if a == b || a == "" || b == "" {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// ovDir represents a resolved overlay directory view (upper + lower layers).
type ovDir struct {
	ino       Ino // exposed inode (RootInode for root, else upperIno or lowerInos[0])
	relPath   string
	upperIno  Ino
	lowerInos []Ino
	opaque    bool
}

type overlayMeta struct {
	Meta
	readOnly bool

	upperSubdir   string // cleaned relative path, e.g. "sandboxes/sb1"
	upperRootIno  Ino
	lowerDirs     []string // cleaned relative paths, highest priority first
	lowerRootInos []Ino
	lowerRootSet  map[Ino]struct{}

	mu         sync.Mutex
	copyUpMap  map[Ino]Ino // lowerIno -> upperIno (only for nodes copied-up/renamed during this mount)
	revCopyUp  map[Ino]Ino // upperIno -> lowerIno (preserves exposed st_ino after copy-up/rename)
	copyUpLock sync.Mutex  // serializes copy-up / ensureUpperDir mutations
}

var _ Meta = (*overlayMeta)(nil)

func validateOverlayPaths(upperSubdir string, lowerDirs []string, readOnly bool) (string, []string, error) {
	if len(lowerDirs) == 0 {
		return "", nil, fmt.Errorf("lower-dir is empty")
	}
	upperClean := cleanRelDir(upperSubdir)
	if !readOnly && upperClean == "" {
		return "", nil, fmt.Errorf("--subdir must be specified and non-root when using --lower-dir")
	}

	cleanedLowers := make([]string, 0, len(lowerDirs))
	for _, d := range lowerDirs {
		c := cleanRelDir(d)
		if c == "" {
			return "", nil, fmt.Errorf("lower-dir cannot be the volume root")
		}
		if upperClean != "" && dirsOverlap(upperClean, c) {
			return "", nil, fmt.Errorf("subdir %q and lower-dir %q must not overlap", upperSubdir, d)
		}
		cleanedLowers = append(cleanedLowers, c)
	}
	return upperClean, cleanedLowers, nil
}

// ValidateOverlayConfig checks overlay mount parameters and verifies that all lowerDirs
// exist and are directories without mutating metadata. Suitable for fail-fast validation.
func ValidateOverlayConfig(ctx Context, base Meta, upperSubdir string, lowerDirs []string, readOnly bool) error {
	_, cleanedLowers, err := validateOverlayPaths(upperSubdir, lowerDirs, readOnly)
	if err != nil {
		return err
	}
	for _, d := range cleanedLowers {
		if _, _, st := resolveDir(ctx, base, d, false); st != 0 {
			return fmt.Errorf("resolve lower-dir %q: %s", d, st)
		}
	}
	return nil
}

// NewOverlayMeta wraps an un-chrooted Meta client with a bounded-memory overlay view:
// lowerDirs (highest priority first) are read-only base directories and
// upperSubdir is the writable upper directory within the same volume.
//
// Exposed inodes are real underlying inodes (since upper and lower live in the same volume),
// keeping memory usage O(copied-up nodes) + small bounded LRU caches rather than O(total files).
func NewOverlayMeta(ctx Context, base Meta, upperSubdir string, lowerDirs []string, readOnly bool) (Meta, error) {
	upperClean, cleanedLowers, err := validateOverlayPaths(upperSubdir, lowerDirs, readOnly)
	if err != nil {
		return nil, err
	}

	lowerRootInos := make([]Ino, 0, len(cleanedLowers))
	lowerRootSet := make(map[Ino]struct{}, len(cleanedLowers))
	for _, d := range cleanedLowers {
		ino, _, st := resolveDir(ctx, base, d, false)
		if st != 0 {
			return nil, fmt.Errorf("resolve lower-dir %q: %s", d, st)
		}
		lowerRootInos = append(lowerRootInos, ino)
		lowerRootSet[ino] = struct{}{}
	}

	var upperRootIno Ino
	if upperClean != "" {
		ino, created, st := resolveDir(ctx, base, upperClean, !readOnly)
		if st != 0 {
			return nil, fmt.Errorf("resolve upper subdir %q: %s", upperSubdir, st)
		}
		upperRootIno = ino
		// Inherit owner and mode from the highest-priority lower root when auto-creating upper subdir,
		// so tools like git do not complain about dubious ownership (uid=0 vs current user).
		if created && len(lowerRootInos) > 0 {
			var lattr Attr
			if base.GetAttr(Background(), lowerRootInos[0], &lattr) == 0 {
				setAttr := lattr
				_ = base.SetAttr(Background(), upperRootIno, SetAttrMode|SetAttrUID|SetAttrGID, 0, &setAttr)
			}
		}
	}

	om := &overlayMeta{
		Meta:          base,
		readOnly:      readOnly,
		upperSubdir:   upperClean,
		upperRootIno:  upperRootIno,
		lowerDirs:     cleanedLowers,
		lowerRootInos: lowerRootInos,
		lowerRootSet:  lowerRootSet,
		copyUpMap:     make(map[Ino]Ino),
		revCopyUp:     make(map[Ino]Ino),
	}
	return om, nil
}

func resolveDir(ctx Context, m Meta, relPath string, create bool) (Ino, bool, syscall.Errno) {
	cur := RootInode
	createdFinal := false
	parts := strings.Split(relPath, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		var ino Ino
		var attr Attr
		st := m.Lookup(ctx, cur, part, &ino, &attr, false)
		if st == syscall.ENOENT && create {
			st = m.Mkdir(ctx, cur, part, 0777, 0, 0, &ino, &attr)
			if st == 0 && i == len(parts)-1 {
				createdFinal = true
			} else if st == syscall.EEXIST {
				st = m.Lookup(ctx, cur, part, &ino, &attr, false)
			}
		}
		if st != 0 {
			return 0, false, st
		}
		if attr.Typ != TypeDirectory {
			return 0, false, syscall.ENOTDIR
		}
		cur = ino
	}
	return cur, createdFinal, 0
}

// resolveActiveIno translates an exposed inode to the real underlying inode to operate on.
func (m *overlayMeta) resolveActiveIno(exposed Ino) Ino {
	if exposed == RootInode {
		if m.upperRootIno != 0 {
			return m.upperRootIno
		}
		return m.lowerRootInos[0]
	}
	m.mu.Lock()
	if u, ok := m.copyUpMap[exposed]; ok {
		m.mu.Unlock()
		return u
	}
	m.mu.Unlock()
	return exposed
}

// exposeUpperIno returns the exposed inode for a given upper inode (preserving lowerIno if copied-up).
func (m *overlayMeta) exposeUpperIno(upperIno Ino) Ino {
	if upperIno == m.upperRootIno {
		return RootInode
	}
	m.mu.Lock()
	if orig, ok := m.revCopyUp[upperIno]; ok {
		m.mu.Unlock()
		return orig
	}
	m.mu.Unlock()
	return upperIno
}

func (m *overlayMeta) mapParentIno(realParent Ino) Ino {
	if realParent == m.upperRootIno {
		return RootInode
	}
	if _, ok := m.lowerRootSet[realParent]; ok {
		return RootInode
	}
	return m.exposeUpperIno(realParent)
}

// bindCopyUpLocked pins exposedIno <-> upperIno so the exposed st_ino stays stable after copy-up or rename.
func (m *overlayMeta) bindCopyUpLocked(exposedIno, upperIno Ino) {
	if exposedIno == RootInode || exposedIno == upperIno {
		return
	}
	if oldUpper, ok := m.copyUpMap[exposedIno]; ok && oldUpper != upperIno {
		delete(m.revCopyUp, oldUpper)
	}
	m.copyUpMap[exposedIno] = upperIno
	m.revCopyUp[upperIno] = exposedIno
}

func (m *overlayMeta) unbindCopyUpLocked(exposedIno Ino) {
	if u, ok := m.copyUpMap[exposedIno]; ok {
		delete(m.copyUpMap, exposedIno)
		delete(m.revCopyUp, u)
	}
}

func (m *overlayMeta) lookupRaw(ctx Context, parent Ino, name string) (Ino, Attr, bool, syscall.Errno) {
	var ino Ino
	var attr Attr
	st := m.Meta.Lookup(ctx, parent, name, &ino, &attr, false)
	if st == syscall.ENOENT {
		return 0, attr, false, 0
	}
	if st != 0 {
		return 0, attr, false, st
	}
	return ino, attr, true, 0
}

func (m *overlayMeta) lookupInLowers(ctx Context, lowerDirs []Ino, name string) (inos []Ino, firstParent Ino, firstAttr Attr, found bool, st syscall.Errno) {
	wh := whiteoutName(name)
	for _, dirIno := range lowerDirs {
		if _, _, hasWh, err := m.lookupRaw(ctx, dirIno, wh); err != 0 {
			return nil, 0, firstAttr, false, err
		} else if hasWh {
			break
		}
		ino, attr, ok, err := m.lookupRaw(ctx, dirIno, name)
		if err != 0 {
			return nil, 0, firstAttr, false, err
		}
		if ok {
			if !found {
				found = true
				firstAttr = attr
				firstParent = dirIno
				inos = append(inos, ino)
				if attr.Typ != TypeDirectory {
					break
				}
			} else {
				if attr.Typ != TypeDirectory {
					break
				}
				inos = append(inos, ino)
			}
			if attr.Typ == TypeDirectory {
				if _, _, isOpq, err := m.lookupRaw(ctx, ino, opaqueMarker); err == 0 && isOpq {
					break
				}
			}
		}
		if _, _, isOpq, err := m.lookupRaw(ctx, dirIno, opaqueMarker); err == 0 && isOpq {
			break
		}
	}
	return inos, firstParent, firstAttr, found, 0
}

// extractRelPath strips the upperSubdir or matching lowerDir prefix from a volume-absolute path.
func (m *overlayMeta) extractRelPath(volPath string) (rel string, isUpper bool, ok bool) {
	c := cleanRelDir(volPath)
	if m.upperSubdir != "" {
		if c == m.upperSubdir {
			return "", true, true
		}
		if strings.HasPrefix(c, m.upperSubdir+"/") {
			return strings.TrimPrefix(c, m.upperSubdir+"/"), true, true
		}
	}
	for _, ld := range m.lowerDirs {
		if c == ld {
			return "", false, true
		}
		if strings.HasPrefix(c, ld+"/") {
			return strings.TrimPrefix(c, ld+"/"), false, true
		}
	}
	return "", false, false
}

// getDir resolves an exposed directory inode to its merged upper/lower *ovDir view.
func (m *overlayMeta) getDir(ctx Context, dirIno Ino) (*ovDir, syscall.Errno) {
	if dirIno == RootInode {
		rootOpaque := false
		if m.upperRootIno != 0 {
			if _, _, exists, st := m.lookupRaw(ctx, m.upperRootIno, opaqueMarker); st == 0 && exists {
				rootOpaque = true
			}
		}
		return &ovDir{
			ino:       RootInode,
			relPath:   "",
			upperIno:  m.upperRootIno,
			lowerInos: m.lowerRootInos,
			opaque:    rootOpaque,
		}, 0
	}

	active := m.resolveActiveIno(dirIno)
	paths := m.Meta.GetPaths(Background(), active)
	var rel string
	found := false
	for _, p := range paths {
		if r, _, ok := m.extractRelPath(p); ok {
			rel = r
			found = true
			break
		}
	}
	if !found {
		return nil, syscall.ENOENT
	}
	return m.buildDirByRelPath(ctx, dirIno, rel)
}

func (m *overlayMeta) buildDirByRelPath(ctx Context, exposedIno Ino, relPath string) (*ovDir, syscall.Errno) {
	if relPath == "" {
		return m.getDir(ctx, RootInode)
	}
	curUpper := m.upperRootIno
	curLowers := m.lowerRootInos
	opaque := false
	if curUpper != 0 {
		if _, _, exists, st := m.lookupRaw(ctx, curUpper, opaqueMarker); st == 0 && exists {
			opaque = true
		}
	}
	for _, part := range strings.Split(relPath, "/") {
		if part == "" {
			continue
		}
		var nextUpper Ino
		nextOpaque := false
		if curUpper != 0 {
			if _, _, hasWh, err := m.lookupRaw(ctx, curUpper, whiteoutName(part)); err != 0 {
				return nil, err
			} else if hasWh {
				return nil, syscall.ENOENT
			}
			ino, a, ok, err := m.lookupRaw(ctx, curUpper, part)
			if err != 0 {
				return nil, err
			}
			if ok {
				if a.Typ != TypeDirectory {
					return nil, syscall.ENOTDIR
				}
				nextUpper = ino
				if _, _, isOpq, err := m.lookupRaw(ctx, ino, opaqueMarker); err == 0 && isOpq {
					nextOpaque = true
				}
			}
		}
		var nextLowers []Ino
		if !opaque && !nextOpaque && len(curLowers) > 0 {
			linos, _, la, ok, err := m.lookupInLowers(ctx, curLowers, part)
			if err != 0 {
				return nil, err
			}
			if ok && la.Typ == TypeDirectory {
				nextLowers = linos
			}
		}
		if nextUpper == 0 && len(nextLowers) == 0 {
			return nil, syscall.ENOENT
		}
		curUpper = nextUpper
		curLowers = nextLowers
		opaque = nextOpaque
	}

	return &ovDir{
		ino:       exposedIno,
		relPath:   relPath,
		upperIno:  curUpper,
		lowerInos: curLowers,
		opaque:    opaque,
	}, 0
}

func (m *overlayMeta) ensureUpperDirByPath(ctx Context, relPath string) (Ino, syscall.Errno) {
	if m.readOnly {
		return 0, syscall.EROFS
	}
	if relPath == "" {
		return m.upperRootIno, 0
	}
	bgCtx := Background()
	curUpper := m.upperRootIno
	curLowers := m.lowerRootInos
	for _, part := range strings.Split(relPath, "/") {
		if part == "" {
			continue
		}
		var nextLowers []Ino
		var lowerAttr Attr
		hasLowerAttr := false
		if len(curLowers) > 0 {
			if linos, _, la, ok, err := m.lookupInLowers(bgCtx, curLowers, part); err == 0 && ok && la.Typ == TypeDirectory {
				nextLowers = linos
				lowerAttr = la
				hasLowerAttr = true
			}
		}
		ino, _, ok, st := m.lookupRaw(bgCtx, curUpper, part)
		if st != 0 {
			return 0, st
		}
		if !ok {
			mode := uint16(0755)
			if hasLowerAttr {
				mode = lowerAttr.Mode & 07777
			}
			var newAttr Attr
			st = m.Meta.Mkdir(bgCtx, curUpper, part, mode, 0, 0, &ino, &newAttr)
			if st == syscall.EEXIST {
				st = m.Meta.Lookup(bgCtx, curUpper, part, &ino, &newAttr, false)
			}
			if st != 0 {
				return 0, st
			}
			if hasLowerAttr {
				setAttr := lowerAttr
				_ = m.Meta.SetAttr(bgCtx, ino, SetAttrMode|SetAttrUID|SetAttrGID, 0, &setAttr)
			}
			// Register newly materialized ancestor directory that shadows a lower
			// directory so exposed st_ino remains stable (prevents getcwd() ENOENT).
			if len(nextLowers) > 0 {
				m.mu.Lock()
				if _, hasRev := m.revCopyUp[ino]; !hasRev {
					m.bindCopyUpLocked(nextLowers[0], ino)
				}
				m.mu.Unlock()
			}
		}
		curUpper = ino
		curLowers = nextLowers
	}
	return curUpper, 0
}

func (m *overlayMeta) ensureUpperDir(ctx Context, dir *ovDir) (Ino, syscall.Errno) {
	if m.readOnly {
		return 0, syscall.EROFS
	}
	if dir.upperIno != 0 {
		return dir.upperIno, 0
	}
	m.copyUpLock.Lock()
	defer m.copyUpLock.Unlock()

	m.mu.Lock()
	if u, ok := m.copyUpMap[dir.ino]; ok {
		m.mu.Unlock()
		return u, 0
	}
	m.mu.Unlock()

	upperIno, st := m.ensureUpperDirByPath(ctx, dir.relPath)
	if st != 0 {
		return 0, st
	}
	m.mu.Lock()
	m.bindCopyUpLocked(dir.ino, upperIno)
	m.mu.Unlock()
	return upperIno, 0
}

// ensureUpperNode ensures an arbitrary exposed inode (file, symlink, or dir) exists in upper.
// If it is already in upper, returns its upperIno directly; otherwise triggers zero-data-copy Copy-Up.
func (m *overlayMeta) ensureUpperNode(ctx Context, exposedIno Ino) (Ino, syscall.Errno) {
	if m.readOnly {
		return 0, syscall.EROFS
	}
	if exposedIno == RootInode {
		return m.upperRootIno, 0
	}

	m.mu.Lock()
	if u, ok := m.copyUpMap[exposedIno]; ok {
		m.mu.Unlock()
		return u, 0
	}
	m.mu.Unlock()

	bgCtx := Background()
	var attr Attr
	if st := m.Meta.GetAttr(bgCtx, exposedIno, &attr); st != 0 {
		return 0, st
	}
	if attr.Typ == TypeDirectory {
		d, st := m.getDir(ctx, exposedIno)
		if st != 0 {
			return 0, st
		}
		return m.ensureUpperDir(ctx, d)
	}

	// Determine whether exposedIno is already in upper or needs Copy-Up from lower.
	var relDir, name string
	var lowerParentIno Ino
	paths := m.Meta.GetPaths(bgCtx, exposedIno)
	matched := false
	for _, p := range paths {
		rel, isUp, ok := m.extractRelPath(p)
		if !ok {
			continue
		}
		if isUp {
			return exposedIno, 0
		}
		dirPart, baseName := path.Split(rel)
		relDir = strings.TrimSuffix(dirPart, "/")
		name = baseName
		lowerParentIno = attr.Parent
		matched = true
		break
	}
	if !matched {
		return exposedIno, 0
	}
	if lowerParentIno == 0 {
		for p := range m.Meta.GetParents(bgCtx, exposedIno) {
			lowerParentIno = p
			break
		}
	}

	m.copyUpLock.Lock()
	defer m.copyUpLock.Unlock()

	m.mu.Lock()
	if u, ok := m.copyUpMap[exposedIno]; ok {
		m.mu.Unlock()
		return u, 0
	}
	m.mu.Unlock()

	upperParentIno, st := m.ensureUpperDirByPath(ctx, relDir)
	if st != 0 {
		return 0, st
	}

	_ = m.Meta.Unlink(bgCtx, upperParentIno, whiteoutName(name), true)

	var count, total uint64
	st = m.Meta.Clone(bgCtx, lowerParentIno, exposedIno, upperParentIno, name, CLONE_MODE_PRESERVE_ATTR, 0, 1, &count, &total)
	if st != 0 && st != syscall.EEXIST {
		return 0, st
	}

	var upperIno Ino
	var upperAttr Attr
	st = m.Meta.Lookup(bgCtx, upperParentIno, name, &upperIno, &upperAttr, false)
	if st != 0 {
		return 0, st
	}

	m.mu.Lock()
	m.bindCopyUpLocked(exposedIno, upperIno)
	m.mu.Unlock()
	return upperIno, 0
}

func (m *overlayMeta) createWhiteout(upperParentIno Ino, name string) syscall.Errno {
	var ino Ino
	var attr Attr
	st := m.Meta.Mknod(Background(), upperParentIno, whiteoutName(name), TypeFile, 0600, 0, 0, "", &ino, &attr)
	if st == syscall.EEXIST {
		return 0
	}
	return st
}

func (m *overlayMeta) createOpaque(upperDirIno Ino) syscall.Errno {
	var ino Ino
	var attr Attr
	st := m.Meta.Mknod(Background(), upperDirIno, opaqueMarker, TypeFile, 0600, 0, 0, "", &ino, &attr)
	if st == syscall.EEXIST {
		return 0
	}
	return st
}

func (m *overlayMeta) Chroot(ctx Context, subdir string, skipCreate bool) syscall.Errno {
	return 0
}

func (m *overlayMeta) Resolve(ctx Context, parent Ino, p string, inode *Ino, attr *Attr, force bool) syscall.Errno {
	return syscall.ENOTSUP
}

func (m *overlayMeta) Lookup(ctx Context, parent Ino, name string, inode *Ino, attr *Attr, checkPerm bool) syscall.Errno {
	if isWhiteoutName(name) {
		return syscall.ENOENT
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}

	if pdir.upperIno != 0 {
		if _, _, hasWh, err := m.lookupRaw(ctx, pdir.upperIno, whiteoutName(name)); err != 0 {
			return err
		} else if hasWh {
			return syscall.ENOENT
		}
		if upperIno, upperAttr, ok, err := m.lookupRaw(ctx, pdir.upperIno, name); err != 0 {
			return err
		} else if ok {
			if inode != nil {
				*inode = m.exposeUpperIno(upperIno)
			}
			if attr != nil {
				upperAttr.Parent = parent
				*attr = upperAttr
			}
			return 0
		}
	}

	if !pdir.opaque && len(pdir.lowerInos) > 0 {
		linos, _, lowerAttr, ok, err := m.lookupInLowers(ctx, pdir.lowerInos, name)
		if err != 0 {
			return err
		}
		if ok {
			if inode != nil {
				*inode = linos[0]
			}
			if attr != nil {
				lowerAttr.Parent = parent
				*attr = lowerAttr
			}
			return 0
		}
	}
	return syscall.ENOENT
}

func (m *overlayMeta) GetAttr(ctx Context, inode Ino, attr *Attr) syscall.Errno {
	target := m.resolveActiveIno(inode)
	st := m.Meta.GetAttr(ctx, target, attr)
	if st == 0 && attr != nil {
		if inode == RootInode {
			attr.Parent = RootInode
		} else {
			attr.Parent = m.mapParentIno(attr.Parent)
		}
	}
	return st
}

func (m *overlayMeta) isInUpper(exposedIno Ino) bool {
	if exposedIno == RootInode {
		return m.upperRootIno != 0
	}
	m.mu.Lock()
	if _, ok := m.copyUpMap[exposedIno]; ok {
		m.mu.Unlock()
		return true
	}
	m.mu.Unlock()

	for _, p := range m.Meta.GetPaths(Background(), exposedIno) {
		if _, isUp, ok := m.extractRelPath(p); ok {
			return isUp
		}
	}
	return false
}

func (m *overlayMeta) SetAttr(ctx Context, inode Ino, set uint16, sugidclearmode uint8, attr *Attr) syscall.Errno {
	if set&^(SetAttrAtime|SetAttrAtimeNow) == 0 {
		// Pure atime updates (e.g. macOS/Linux read atime updates) must not trigger Copy-Up.
		if !m.isInUpper(inode) {
			return m.GetAttr(ctx, inode, attr)
		}
		st := m.Meta.SetAttr(ctx, m.resolveActiveIno(inode), set, sugidclearmode, attr)
		if st == 0 && attr != nil {
			if inode == RootInode {
				attr.Parent = RootInode
			} else {
				attr.Parent = m.mapParentIno(attr.Parent)
			}
		}
		return st
	}
	target, st := m.ensureUpperNode(ctx, inode)
	if st != 0 {
		return st
	}
	st = m.Meta.SetAttr(ctx, target, set, sugidclearmode, attr)
	if st == 0 && attr != nil {
		if inode == RootInode {
			attr.Parent = RootInode
		} else {
			attr.Parent = m.mapParentIno(attr.Parent)
		}
	}
	return st
}

func (m *overlayMeta) CheckSetAttr(ctx Context, inode Ino, set uint16, attr Attr) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	return m.Meta.CheckSetAttr(ctx, m.resolveActiveIno(inode), set, attr)
}

func (m *overlayMeta) Truncate(ctx Context, inode Ino, flags uint8, attrlength uint64, attr *Attr, skipPermCheck bool) syscall.Errno {
	target, st := m.ensureUpperNode(ctx, inode)
	if st != 0 {
		return st
	}
	return m.Meta.Truncate(ctx, target, flags, attrlength, attr, skipPermCheck)
}

func (m *overlayMeta) Fallocate(ctx Context, inode Ino, mode uint8, off uint64, size uint64, length *uint64) syscall.Errno {
	target, st := m.ensureUpperNode(ctx, inode)
	if st != 0 {
		return st
	}
	return m.Meta.Fallocate(ctx, target, mode, off, size, length)
}

func (m *overlayMeta) Access(ctx Context, inode Ino, modemask uint8, attr *Attr) syscall.Errno {
	return m.Meta.Access(ctx, m.resolveActiveIno(inode), modemask, attr)
}

func (m *overlayMeta) ReadLink(ctx Context, inode Ino, target *[]byte) syscall.Errno {
	return m.Meta.ReadLink(ctx, m.resolveActiveIno(inode), target)
}

func (m *overlayMeta) Open(ctx Context, inode Ino, flags uint32, attr *Attr) syscall.Errno {
	isWrite := flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0
	var target Ino
	if isWrite {
		var st syscall.Errno
		target, st = m.ensureUpperNode(ctx, inode)
		if st != 0 {
			return st
		}
	} else {
		target = m.resolveActiveIno(inode)
	}
	return m.Meta.Open(ctx, target, flags, attr)
}

func (m *overlayMeta) Close(ctx Context, inode Ino) syscall.Errno {
	return m.Meta.Close(ctx, m.resolveActiveIno(inode))
}

func (m *overlayMeta) Read(ctx Context, inode Ino, indx uint32, slices *[]Slice) syscall.Errno {
	return m.Meta.Read(ctx, m.resolveActiveIno(inode), indx, slices)
}

func (m *overlayMeta) Write(ctx Context, inode Ino, indx uint32, off uint32, slice Slice, mtime time.Time) syscall.Errno {
	target, st := m.ensureUpperNode(ctx, inode)
	if st != 0 {
		return st
	}
	return m.Meta.Write(ctx, target, indx, off, slice, mtime)
}

func (m *overlayMeta) InvalidateChunkCache(ctx Context, inode Ino, indx uint32) syscall.Errno {
	return m.Meta.InvalidateChunkCache(ctx, m.resolveActiveIno(inode), indx)
}

func (m *overlayMeta) CopyFileRange(ctx Context, fin Ino, offIn uint64, fout Ino, offOut uint64, size uint64, flags uint32, copied, outLength *uint64) syscall.Errno {
	dstIno, st := m.ensureUpperNode(ctx, fout)
	if st != 0 {
		return st
	}
	return m.Meta.CopyFileRange(ctx, m.resolveActiveIno(fin), offIn, dstIno, offOut, size, flags, copied, outLength)
}

func (m *overlayMeta) Create(ctx Context, parent Ino, name string, mode uint16, cumask uint16, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(name) {
		return syscall.EPERM
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}

	var existingIno Ino
	var existingAttr Attr
	if m.Lookup(ctx, parent, name, &existingIno, &existingAttr, false) == 0 {
		if flags&syscall.O_EXCL != 0 {
			return syscall.EEXIST
		}
		if existingAttr.Typ == TypeDirectory {
			return syscall.EISDIR
		}
		if st = m.Open(ctx, existingIno, flags, &existingAttr); st != 0 {
			return st
		}
		if inode != nil {
			*inode = existingIno
		}
		if attr != nil {
			*attr = existingAttr
		}
		return 0
	}

	upperParent, st := m.ensureUpperDir(ctx, pdir)
	if st != 0 {
		return st
	}

	_ = m.Meta.Unlink(Background(), upperParent, whiteoutName(name), true)

	var realIno Ino
	var realAttr Attr
	st = m.Meta.Create(ctx, upperParent, name, mode, cumask, flags, &realIno, &realAttr)
	if st != 0 {
		return st
	}
	if inode != nil {
		*inode = realIno
	}
	if attr != nil {
		realAttr.Parent = parent
		*attr = realAttr
	}
	return 0
}

func (m *overlayMeta) Mknod(ctx Context, parent Ino, name string, _type uint8, mode uint16, cumask uint16, rdev uint32, p string, inode *Ino, attr *Attr) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(name) {
		return syscall.EPERM
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}
	if m.Lookup(ctx, parent, name, nil, nil, false) == 0 {
		return syscall.EEXIST
	}
	upperParent, st := m.ensureUpperDir(ctx, pdir)
	if st != 0 {
		return st
	}
	_ = m.Meta.Unlink(Background(), upperParent, whiteoutName(name), true)

	var realIno Ino
	var realAttr Attr
	st = m.Meta.Mknod(ctx, upperParent, name, _type, mode, cumask, rdev, p, &realIno, &realAttr)
	if st != 0 {
		return st
	}
	if inode != nil {
		*inode = realIno
	}
	if attr != nil {
		realAttr.Parent = parent
		*attr = realAttr
	}
	return 0
}

func (m *overlayMeta) Mkdir(ctx Context, parent Ino, name string, mode uint16, cumask uint16, copysgid uint8, inode *Ino, attr *Attr) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(name) {
		return syscall.EPERM
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}
	if m.Lookup(ctx, parent, name, nil, nil, false) == 0 {
		return syscall.EEXIST
	}
	upperParent, st := m.ensureUpperDir(ctx, pdir)
	if st != 0 {
		return st
	}

	hadLowerDir := false
	if !pdir.opaque && len(pdir.lowerInos) > 0 {
		_, _, la, ok, err := m.lookupInLowers(ctx, pdir.lowerInos, name)
		if err == 0 && ok && la.Typ == TypeDirectory {
			hadLowerDir = true
		}
	}

	_ = m.Meta.Unlink(Background(), upperParent, whiteoutName(name), true)

	var realIno Ino
	var realAttr Attr
	st = m.Meta.Mkdir(ctx, upperParent, name, mode, cumask, copysgid, &realIno, &realAttr)
	if st != 0 {
		return st
	}
	if hadLowerDir {
		if st = m.createOpaque(realIno); st != 0 {
			return st
		}
	}
	if inode != nil {
		*inode = realIno
	}
	if attr != nil {
		realAttr.Parent = parent
		*attr = realAttr
	}
	return 0
}

func (m *overlayMeta) Symlink(ctx Context, parent Ino, name string, target string, inode *Ino, attr *Attr) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(name) {
		return syscall.EPERM
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}
	if m.Lookup(ctx, parent, name, nil, nil, false) == 0 {
		return syscall.EEXIST
	}
	upperParent, st := m.ensureUpperDir(ctx, pdir)
	if st != 0 {
		return st
	}
	_ = m.Meta.Unlink(Background(), upperParent, whiteoutName(name), true)

	var realIno Ino
	var realAttr Attr
	st = m.Meta.Symlink(ctx, upperParent, name, target, &realIno, &realAttr)
	if st != 0 {
		return st
	}
	if inode != nil {
		*inode = realIno
	}
	if attr != nil {
		realAttr.Parent = parent
		*attr = realAttr
	}
	return 0
}

func (m *overlayMeta) Link(ctx Context, inodeSrc, parent Ino, name string, attr *Attr) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(name) {
		return syscall.EPERM
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}
	if m.Lookup(ctx, parent, name, nil, nil, false) == 0 {
		return syscall.EEXIST
	}
	srcUpper, st := m.ensureUpperNode(ctx, inodeSrc)
	if st != 0 {
		return st
	}
	upperParent, st := m.ensureUpperDir(ctx, pdir)
	if st != 0 {
		return st
	}
	_ = m.Meta.Unlink(Background(), upperParent, whiteoutName(name), true)

	var realAttr Attr
	st = m.Meta.Link(ctx, srcUpper, upperParent, name, &realAttr)
	if st != 0 {
		return st
	}
	// Exposed inode for srcUpper automatically resolves back to inodeSrc via revCopyUp (or srcUpper itself).
	if attr != nil {
		realAttr.Parent = parent
		*attr = realAttr
	}
	return 0
}

func (m *overlayMeta) Unlink(ctx Context, parent Ino, name string, skipCheckTrash ...bool) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(name) {
		return syscall.ENOENT
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}
	var childIno Ino
	var childAttr Attr
	if st = m.Lookup(ctx, parent, name, &childIno, &childAttr, false); st != 0 {
		return st
	}
	if childAttr.Typ == TypeDirectory {
		return syscall.EISDIR
	}
	upperParent, st := m.ensureUpperDir(ctx, pdir)
	if st != 0 {
		return st
	}

	if _, _, ok, err := m.lookupRaw(ctx, upperParent, name); err != 0 {
		return err
	} else if ok {
		if st = m.Meta.Unlink(ctx, upperParent, name, true); st != 0 {
			return st
		}
	}

	if !pdir.opaque && len(pdir.lowerInos) > 0 {
		if _, _, _, existsInLower, err := m.lookupInLowers(ctx, pdir.lowerInos, name); err == 0 && existsInLower {
			if st = m.createWhiteout(upperParent, name); st != 0 {
				return st
			}
		}
	}
	return 0
}

func (m *overlayMeta) Rmdir(ctx Context, parent Ino, name string, skipCheckTrash ...bool) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(name) {
		return syscall.ENOENT
	}
	pdir, st := m.getDir(ctx, parent)
	if st != 0 {
		return st
	}
	var childIno Ino
	var childAttr Attr
	if st = m.Lookup(ctx, parent, name, &childIno, &childAttr, false); st != 0 {
		return st
	}
	if childAttr.Typ != TypeDirectory {
		return syscall.ENOTDIR
	}

	var entries []*Entry
	if st = m.Readdir(ctx, childIno, 0, &entries); st != 0 {
		return st
	}
	if len(entries) > 0 {
		return syscall.ENOTEMPTY
	}

	upperParent, st := m.ensureUpperDir(ctx, pdir)
	if st != 0 {
		return st
	}

	if upperChildIno, _, ok, err := m.lookupRaw(ctx, upperParent, name); err != 0 {
		return err
	} else if ok {
		bgCtx := Background()
		var rawEntries []*Entry
		if st = m.Meta.Readdir(bgCtx, upperChildIno, 0, &rawEntries); st != 0 {
			return st
		}
		for _, e := range rawEntries {
			en := string(e.Name)
			if en == "." || en == ".." {
				continue
			}
			if isWhiteoutName(en) {
				if st = m.Meta.Unlink(bgCtx, upperChildIno, en, true); st != 0 {
					return st
				}
			}
		}
		if st = m.Meta.Rmdir(ctx, upperParent, name, true); st != 0 {
			return st
		}
	}

	if !pdir.opaque && len(pdir.lowerInos) > 0 {
		if _, _, _, existsInLower, err := m.lookupInLowers(ctx, pdir.lowerInos, name); err == 0 && existsInLower {
			if st = m.createWhiteout(upperParent, name); st != 0 {
				return st
			}
		}
	}

	m.mu.Lock()
	m.unbindCopyUpLocked(childIno)
	m.mu.Unlock()
	return 0
}

func (m *overlayMeta) Rename(ctx Context, parentSrc Ino, nameSrc string, parentDst Ino, nameDst string, flags uint32, inode *Ino, attr *Attr) syscall.Errno {
	if m.readOnly {
		return syscall.EROFS
	}
	if isWhiteoutName(nameSrc) || isWhiteoutName(nameDst) {
		return syscall.EPERM
	}
	if flags&(RenameExchange|RenameWhiteout) != 0 {
		return syscall.ENOTSUP
	}
	if parentSrc == parentDst && nameSrc == nameDst {
		return 0
	}
	srcParent, st := m.getDir(ctx, parentSrc)
	if st != 0 {
		return st
	}
	dstParent, st := m.getDir(ctx, parentDst)
	if st != 0 {
		return st
	}

	var srcIno Ino
	var srcAttr Attr
	if st = m.Lookup(ctx, parentSrc, nameSrc, &srcIno, &srcAttr, false); st != 0 {
		return st
	}

	var dstIno Ino
	var dstAttr Attr
	dstExists := m.Lookup(ctx, parentDst, nameDst, &dstIno, &dstAttr, false) == 0
	if dstExists && flags&RenameNoReplace != 0 {
		return syscall.EEXIST
	}
	if dstExists && dstAttr.Typ == TypeDirectory {
		if srcAttr.Typ != TypeDirectory {
			return syscall.EISDIR
		}
		var entries []*Entry
		if st = m.Readdir(ctx, dstIno, 0, &entries); st != 0 {
			return st
		}
		if len(entries) > 0 {
			return syscall.ENOTEMPTY
		}
	}

	upperSrcParent, st := m.ensureUpperDir(ctx, srcParent)
	if st != 0 {
		return st
	}
	upperDstParent, st := m.ensureUpperDir(ctx, dstParent)
	if st != 0 {
		return st
	}

	var srcDir *ovDir
	if srcAttr.Typ == TypeDirectory {
		srcDir, st = m.getDir(ctx, srcIno)
		if st != 0 {
			return st
		}
		if len(srcDir.lowerInos) > 0 {
			return m.renameLowerDir(ctx, srcParent, upperSrcParent, nameSrc, srcDir, dstParent, upperDstParent, nameDst, dstExists, dstAttr, inode, attr)
		}
	} else {
		if _, st = m.ensureUpperNode(ctx, srcIno); st != 0 {
			return st
		}
	}

	if dstExists && dstAttr.Typ == TypeDirectory {
		if st = m.Rmdir(ctx, parentDst, nameDst, true); st != 0 {
			return st
		}
	}
	_ = m.Meta.Unlink(Background(), upperDstParent, whiteoutName(nameDst), true)

	var realIno Ino
	var realAttr Attr
	st = m.Meta.Rename(ctx, upperSrcParent, nameSrc, upperDstParent, nameDst, flags, &realIno, &realAttr)
	if st != 0 {
		return st
	}

	if !srcParent.opaque && len(srcParent.lowerInos) > 0 {
		if _, _, _, existsInLower, err := m.lookupInLowers(ctx, srcParent.lowerInos, nameSrc); err == 0 && existsInLower {
			if st = m.createWhiteout(upperSrcParent, nameSrc); st != 0 {
				return st
			}
		}
	}

	if srcAttr.Typ == TypeDirectory && !srcDir.opaque && !dstParent.opaque && len(dstParent.lowerInos) > 0 {
		if _, _, la, ok, err := m.lookupInLowers(ctx, dstParent.lowerInos, nameDst); err == 0 && ok && la.Typ == TypeDirectory {
			if st = m.createOpaque(realIno); st != 0 {
				return st
			}
		}
	}

	if inode != nil {
		*inode = srcIno
	}
	if attr != nil {
		realAttr.Parent = parentDst
		*attr = realAttr
	}
	return 0
}

func (m *overlayMeta) copyMergedDirToUpper(ctx Context, srcDir *ovDir, upperDstParent Ino, nameDst string, markOpaque bool) (Ino, Attr, syscall.Errno) {
	bgCtx := Background()
	var srcAttr Attr
	if st := m.GetAttr(bgCtx, srcDir.ino, &srcAttr); st != 0 {
		return 0, srcAttr, st
	}
	var dstDirIno Ino
	var dstDirAttr Attr
	st := m.Meta.Mkdir(bgCtx, upperDstParent, nameDst, srcAttr.Mode&07777, 0, 0, &dstDirIno, &dstDirAttr)
	if st != 0 {
		return 0, dstDirAttr, st
	}
	setAttr := srcAttr
	_ = m.Meta.SetAttr(bgCtx, dstDirIno, SetAttrMode|SetAttrUID|SetAttrGID, 0, &setAttr)
	if markOpaque {
		if st = m.createOpaque(dstDirIno); st != 0 {
			return 0, dstDirAttr, st
		}
	}

	var entries []*Entry
	if st = m.Readdir(bgCtx, srcDir.ino, 1, &entries); st != 0 {
		return 0, dstDirAttr, st
	}
	for _, e := range entries {
		childName := string(e.Name)
		if e.Attr != nil && e.Attr.Typ == TypeDirectory {
			childDir, st := m.getDir(bgCtx, e.Inode)
			if st != 0 {
				return 0, dstDirAttr, st
			}
			if _, _, st = m.copyMergedDirToUpper(ctx, childDir, dstDirIno, childName, false); st != 0 {
				return 0, dstDirAttr, st
			}
		} else {
			srcFileIno := m.resolveActiveIno(e.Inode)
			var srcFileAttr Attr
			if st = m.Meta.GetAttr(bgCtx, srcFileIno, &srcFileAttr); st != 0 {
				return 0, dstDirAttr, st
			}
			srcParentIno := srcFileAttr.Parent
			if srcParentIno == 0 {
				for p := range m.Meta.GetParents(bgCtx, srcFileIno) {
					srcParentIno = p
					break
				}
			}
			var count, total uint64
			if st = m.Meta.Clone(bgCtx, srcParentIno, srcFileIno, dstDirIno, childName, CLONE_MODE_PRESERVE_ATTR, 0, 1, &count, &total); st != 0 {
				return 0, dstDirAttr, st
			}
		}
	}
	_ = m.Meta.GetAttr(bgCtx, dstDirIno, &dstDirAttr)
	return dstDirIno, dstDirAttr, 0
}

func (m *overlayMeta) renameLowerDir(
	ctx Context,
	srcParent *ovDir, upperSrcParent Ino, nameSrc string, srcDir *ovDir,
	dstParent *ovDir, upperDstParent Ino, nameDst string,
	dstExists bool, dstAttr Attr,
	inode *Ino, attr *Attr,
) syscall.Errno {
	bgCtx := Background()
	if dstExists && dstAttr.Typ == TypeDirectory {
		if st := m.Rmdir(ctx, dstParent.ino, nameDst, true); st != 0 {
			return st
		}
	}
	_ = m.Meta.Unlink(bgCtx, upperDstParent, whiteoutName(nameDst), true)

	needOpaque := false
	if !dstParent.opaque && len(dstParent.lowerInos) > 0 {
		if _, _, la, ok, err := m.lookupInLowers(bgCtx, dstParent.lowerInos, nameDst); err == 0 && ok && la.Typ == TypeDirectory {
			needOpaque = true
		}
	}

	dstDirIno, dstDirAttr, st := m.copyMergedDirToUpper(ctx, srcDir, upperDstParent, nameDst, needOpaque)
	if st != 0 {
		return st
	}

	if _, _, hasUpperSrc, err := m.lookupRaw(bgCtx, upperSrcParent, nameSrc); err == 0 && hasUpperSrc {
		var count uint64
		if st = m.Meta.Remove(bgCtx, upperSrcParent, nameSrc, true, 1, &count); st != 0 {
			return st
		}
	}

	if st = m.createWhiteout(upperSrcParent, nameSrc); st != 0 {
		return st
	}

	// Keep srcDir.ino mapped to dstDirIno: FUSE rename does not return a new inode number,
	// so the kernel continues referencing the renamed directory via srcDir.ino.
	m.mu.Lock()
	m.bindCopyUpLocked(srcDir.ino, dstDirIno)
	m.mu.Unlock()

	if inode != nil {
		*inode = srcDir.ino
	}
	if attr != nil {
		dstDirAttr.Parent = dstParent.ino
		*attr = dstDirAttr
	}
	return 0
}

func (m *overlayMeta) Readdir(ctx Context, inode Ino, wantattr uint8, entries *[]*Entry) syscall.Errno {
	dir, st := m.getDir(ctx, inode)
	if st != 0 {
		return st
	}

	seen := make(map[string]*Entry)
	whiteouts := make(map[string]struct{})
	order := make([]string, 0)
	opaque := dir.opaque

	processLayer := func(layerDir Ino, isUpper bool) (stopLower bool, err syscall.Errno) {
		var raw []*Entry
		if st := m.Meta.Readdir(ctx, layerDir, wantattr, &raw); st != 0 {
			return false, st
		}
		for _, e := range raw {
			name := string(e.Name)
			if name == "." || name == ".." {
				continue
			}
			if name == opaqueMarker {
				stopLower = true
				continue
			}
			if strings.HasPrefix(name, whiteoutPrefix) {
				target := strings.TrimPrefix(name, whiteoutPrefix)
				if _, already := seen[target]; !already {
					whiteouts[target] = struct{}{}
				}
				continue
			}
			if _, wh := whiteouts[name]; wh {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			var attr *Attr
			if e.Attr != nil {
				attrCopy := *e.Attr
				attrCopy.Parent = inode
				attr = &attrCopy
			}
			exposedIno := e.Inode
			if isUpper {
				exposedIno = m.exposeUpperIno(e.Inode)
			}
			seen[name] = &Entry{
				Inode: exposedIno,
				Name:  []byte(name),
				Attr:  attr,
			}
			order = append(order, name)
		}
		return stopLower, 0
	}

	if dir.upperIno != 0 {
		stop, err := processLayer(dir.upperIno, true)
		if err != 0 {
			return err
		}
		if stop {
			opaque = true
		}
	}

	if !opaque {
		for _, ld := range dir.lowerInos {
			stop, err := processLayer(ld, false)
			if err != 0 {
				return err
			}
			if stop {
				break
			}
		}
	}

	sort.Strings(order)
	result := make([]*Entry, 0, len(order))
	for _, name := range order {
		result = append(result, seen[name])
	}
	*entries = result
	return 0
}

type overlayDirHandler struct {
	mu      sync.Mutex
	entries []*Entry
}

func (h *overlayDirHandler) List(ctx Context, offset int) ([]*Entry, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if offset >= len(h.entries) {
		return nil, 0
	}
	return h.entries[offset:], 0
}

func (h *overlayDirHandler) Insert(inode Ino, name string, attr *Attr) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, &Entry{Inode: inode, Name: []byte(name), Attr: attr})
}

func (h *overlayDirHandler) Delete(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, e := range h.entries {
		if string(e.Name) == name {
			h.entries = append(h.entries[:i], h.entries[i+1:]...)
			return
		}
	}
}

func (h *overlayDirHandler) Read(offset int) {}

func (h *overlayDirHandler) Close() {}

func (m *overlayMeta) NewDirHandler(ctx Context, inode Ino, plus bool, initEntries []*Entry) (DirHandler, syscall.Errno) {
	var attr Attr
	if st := m.GetAttr(ctx, inode, &attr); st != 0 {
		return nil, st
	}
	if inode == RootInode {
		attr.Parent = RootInode
	}
	all := make([]*Entry, 0, len(initEntries)+2)
	all = append(all, initEntries...)
	all = append(all, &Entry{
		Inode: inode,
		Name:  []byte("."),
		Attr:  &attr,
	})
	parentEntry := &Entry{
		Inode: attr.Parent,
		Name:  []byte(".."),
		Attr:  &Attr{Typ: TypeDirectory},
	}
	if plus {
		if attr.Parent == inode {
			parentEntry.Attr = &attr
		} else if st := m.GetAttr(ctx, attr.Parent, parentEntry.Attr); st != 0 {
			return nil, st
		}
	}
	all = append(all, parentEntry)

	wantAttr := uint8(0)
	if plus {
		wantAttr = 1
	}
	var entries []*Entry
	if st := m.Readdir(ctx, inode, wantAttr, &entries); st != 0 {
		return nil, st
	}
	all = append(all, entries...)
	return &overlayDirHandler{entries: all}, 0
}

func (m *overlayMeta) GetXattr(ctx Context, inode Ino, name string, vbuff *[]byte) syscall.Errno {
	return m.Meta.GetXattr(ctx, m.resolveActiveIno(inode), name, vbuff)
}

func (m *overlayMeta) ListXattr(ctx Context, inode Ino, dbuff *[]byte) syscall.Errno {
	return m.Meta.ListXattr(ctx, m.resolveActiveIno(inode), dbuff)
}

func (m *overlayMeta) SetXattr(ctx Context, inode Ino, name string, value []byte, flags uint32) syscall.Errno {
	target, st := m.ensureUpperNode(ctx, inode)
	if st != 0 {
		return st
	}
	return m.Meta.SetXattr(ctx, target, name, value, flags)
}

func (m *overlayMeta) RemoveXattr(ctx Context, inode Ino, name string) syscall.Errno {
	target, st := m.ensureUpperNode(ctx, inode)
	if st != 0 {
		return st
	}
	return m.Meta.RemoveXattr(ctx, target, name)
}

func (m *overlayMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	return m.Meta.Flock(ctx, m.resolveActiveIno(inode), owner, ltype, block)
}

func (m *overlayMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	return m.Meta.Getlk(ctx, m.resolveActiveIno(inode), owner, ltype, start, end, pid)
}

func (m *overlayMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	return m.Meta.Setlk(ctx, m.resolveActiveIno(inode), owner, block, ltype, start, end, pid)
}

func (m *overlayMeta) GetFacl(ctx Context, ino Ino, aclType uint8, n *aclAPI.Rule) syscall.Errno {
	return m.Meta.GetFacl(ctx, m.resolveActiveIno(ino), aclType, n)
}

func (m *overlayMeta) SetFacl(ctx Context, ino Ino, aclType uint8, n *aclAPI.Rule) syscall.Errno {
	target, st := m.ensureUpperNode(ctx, ino)
	if st != 0 {
		return st
	}
	return m.Meta.SetFacl(ctx, target, aclType, n)
}

func (m *overlayMeta) GetPaths(ctx Context, inode Ino) []string {
	if inode == RootInode {
		return []string{"/"}
	}
	if inode == TrashInode {
		return []string{"/.trash"}
	}
	rawPaths := m.Meta.GetPaths(ctx, m.resolveActiveIno(inode))
	seen := make(map[string]struct{}, len(rawPaths))
	res := make([]string, 0, len(rawPaths))
	for _, p := range rawPaths {
		rel, _, ok := m.extractRelPath(p)
		if !ok {
			continue
		}
		vpath := "/"
		if rel != "" {
			vpath = "/" + rel
		}
		if _, dup := seen[vpath]; !dup {
			seen[vpath] = struct{}{}
			res = append(res, vpath)
		}
	}
	return res
}

func (m *overlayMeta) GetSummary(ctx Context, inode Ino, summary *Summary, recursive bool, strict bool) syscall.Errno {
	var attr Attr
	if st := m.GetAttr(ctx, inode, &attr); st != 0 {
		return st
	}
	if attr.Typ != TypeDirectory {
		summary.Files++
		summary.Size += uint64(align4K(attr.Length))
		if attr.Typ == TypeFile {
			summary.Length += attr.Length
		}
		return 0
	}
	summary.Dirs++
	summary.Size += uint64(align4K(0))
	var entries []*Entry
	if st := m.Readdir(ctx, inode, 1, &entries); st != 0 {
		return st
	}
	for _, e := range entries {
		if e.Attr == nil {
			continue
		}
		if e.Attr.Typ == TypeDirectory {
			if recursive {
				if st := m.GetSummary(ctx, e.Inode, summary, true, strict); st != 0 {
					return st
				}
			} else {
				summary.Dirs++
				summary.Size += uint64(align4K(0))
			}
		} else {
			summary.Files++
			summary.Size += uint64(align4K(e.Attr.Length))
			if e.Attr.Typ == TypeFile {
				summary.Length += e.Attr.Length
			}
		}
	}
	return 0
}

func (m *overlayMeta) StatFS(ctx Context, ino Ino, totalspace, availspace, iused, iavail *uint64) syscall.Errno {
	return m.Meta.StatFS(ctx, m.resolveActiveIno(RootInode), totalspace, availspace, iused, iavail)
}

func (m *overlayMeta) IsUnderRoot(ctx Context, inode Ino) bool {
	if inode == RootInode {
		return true
	}
	paths := m.Meta.GetPaths(Background(), m.resolveActiveIno(inode))
	for _, p := range paths {
		if _, _, ok := m.extractRelPath(p); ok {
			return true
		}
	}
	return false
}
