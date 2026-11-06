//go:build !libjfs

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

package juicefs

import (
	"fmt"
	"syscall"

	jfs "github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

func (b *fsBackend) getxattr(p, attr string) ([]byte, error) {
	data, e := b.fs.GetXattr(b.ctx(), p, attr)
	return data, errno(e)
}

func (b *fsBackend) listxattr(p string) ([]byte, error) {
	names, e := b.fs.ListXattr(b.ctx(), p)
	return names, errno(e)
}

func (b *fsBackend) setxattr(p, attr string, data []byte, flags XattrFlag) error {
	var f uint32
	switch flags {
	case XattrCreate:
		f = meta.XattrCreate
	case XattrReplace:
		f = meta.XattrReplace
	}
	return errno(b.fs.SetXattr(b.ctx(), p, attr, data, f))
}

func (b *fsBackend) removexattr(p, attr string) error {
	return errno(b.fs.RemoveXattr(b.ctx(), p, attr))
}

func (b *fsBackend) clone(src, dst string, preserve bool) error {
	return errno(b.fs.Clone(b.ctx(), src, dst, preserve))
}

func (b *fsBackend) info(p string, recursive, strict bool) (*InfoResult, error) {
	var r *InfoResult
	err := b.withFile(p, true, func(ctx meta.Context, f *jfs.File) syscall.Errno {
		s, e := f.Summary(ctx, recursive, strict)
		if e == 0 {
			r = &InfoResult{Length: s.Length, Size: s.Size, Files: s.Files, Dirs: s.Dirs}
		}
		return e
	})
	return r, err
}

func (b *fsBackend) summary(p string, depth, entries uint8) (*TreeSummary, error) {
	var r *TreeSummary
	err := b.withFile(p, true, func(ctx meta.Context, f *jfs.File) syscall.Errno {
		s, e := f.GetTreeSummary(ctx, depth, entries, true)
		if e == 0 {
			r = toTreeSummary(s)
		}
		return e
	})
	return r, err
}

func toTreeSummary(s *meta.TreeSummary) *TreeSummary {
	t := &TreeSummary{
		Path:  s.Path,
		Type:  fileMode((&meta.Attr{Typ: s.Type}).SMode()).Type(),
		Size:  s.Size,
		Files: s.Files,
		Dirs:  s.Dirs,
	}
	for _, c := range s.Children {
		t.Children = append(t.Children, toTreeSummary(c))
	}
	return t
}

func (b *fsBackend) quota(cmd uint8, name string, capacity, inodes uint64, strict, repair, create bool) (map[string]*Quota, error) {
	qs, e := b.fs.HandleQuota(b.ctx(), name, cmd, capacity, inodes, strict, repair, create)
	if e != 0 {
		return nil, e
	}
	r := make(map[string]*Quota, len(qs))
	for k, q := range qs {
		r[k] = &Quota{MaxSpace: q.MaxSpace, MaxInodes: q.MaxInodes, UsedSpace: q.UsedSpace, UsedInodes: q.UsedInodes}
	}
	return r, nil
}

func (b *fsBackend) warmup(paths []string, threads int, evict, check, background bool) (*WarmupResult, error) {
	resp := &vfs.CacheResponse{Locations: make(map[string]uint64)}
	b.fs.Warmup(b.ctx(), paths, threads, background, evict, check, resp)
	if background {
		return nil, nil
	}
	return &WarmupResult{
		FileCount:  resp.FileCount,
		SliceCount: resp.SliceCount,
		TotalBytes: resp.TotalBytes,
		MissBytes:  resp.MissBytes,
		Locations:  resp.Locations,
	}, nil
}

func (b *fsBackend) status(trash bool) (*StatusResult, error) {
	var s meta.Sections
	if err := meta.Status(b.ctx(), b.fs.Meta(), trash, &s); err != nil {
		logger.Errorf("status: %s", err)
		return nil, syscall.EIO
	}
	r := &StatusResult{
		Setting: toVolumeSetting(s.Setting),
		Stat: VolumeStat{
			UsedSpace:       s.Stat.UsedSpace,
			AvailableSpace:  s.Stat.AvailableSpace,
			UsedInodes:      s.Stat.UsedInodes,
			AvailableInodes: s.Stat.AvailableInodes,
		},
	}
	for _, ss := range s.Sessions {
		r.Sessions = append(r.Sessions, toSession(ss))
	}
	if trash {
		st := s.Stat
		r.Stat.Trash = &TrashStat{
			TrashFileCount:           st.TrashFileCount,
			TrashFileSize:            st.TrashFileSize,
			PendingDeletedFileCount:  st.PendingDeletedFileCount,
			PendingDeletedFileSize:   st.PendingDeletedFileSize,
			TrashSliceCount:          st.TrashSliceCount,
			TrashSliceSize:           st.TrashSliceSize,
			PendingDeletedSliceCount: st.PendingDeletedSliceCount,
			PendingDeletedSliceSize:  st.PendingDeletedSliceSize,
		}
	}
	return r, nil
}

func toVolumeSetting(f *meta.Format) VolumeSetting {
	s := VolumeSetting{
		Name:          f.Name,
		UUID:          f.UUID,
		Storage:       f.Storage,
		Bucket:        utils.RemovePassword(f.Bucket),
		StorageClass:  f.Tiers[0].Sc,
		BlockSize:     f.BlockSize << 10,
		Compression:   f.Compression,
		Capacity:      f.Capacity,
		Inodes:        f.Inodes,
		TrashDays:     f.TrashDays,
		UploadLimit:   f.UploadLimit,
		DownloadLimit: f.DownloadLimit,
		EnableACL:     f.EnableACL,
	}
	if f.EncryptKey != "" {
		s.EncryptAlgo = f.EncryptAlgo
	}
	return s
}

func (b *fsBackend) sessionInfo(sid uint64) (*Session, error) {
	s, err := b.fs.Meta().GetSession(sid, true)
	if err != nil {
		logger.Errorf("get session %d: %s", sid, err)
		return nil, syscall.EIO
	}
	return toSession(s), nil
}

func toSession(s *meta.Session) *Session {
	r := &Session{
		Sid:        s.Sid,
		Expire:     s.Expire,
		Version:    s.Version,
		HostName:   s.HostName,
		IPAddrs:    s.IPAddrs,
		MountPoint: s.MountPoint,
		MountTime:  s.MountTime,
		ProcessID:  s.ProcessID,
	}
	for _, inode := range s.Sustained {
		r.Sustained = append(r.Sustained, uint64(inode))
	}
	for _, l := range s.Flocks {
		r.Flocks = append(r.Flocks, Flock{Inode: uint64(l.Inode), Owner: l.Owner, Type: l.Ltype})
	}
	for _, l := range s.Plocks {
		p := Plock{Inode: uint64(l.Inode), Owner: l.Owner}
		for _, rec := range l.Records {
			p.Records = append(p.Records, PlockRecord{Type: plockType(rec.Type), Pid: rec.Pid, Start: rec.Start, End: rec.End})
		}
		r.Plocks = append(r.Plocks, p)
	}
	return r
}

// plockType converts F_RDLCK and F_WRLCK, whose values differ by platform.
func plockType(t uint32) string {
	switch t {
	case meta.F_RDLCK:
		return "R"
	case meta.F_WRLCK:
		return "W"
	}
	return fmt.Sprint(t)
}
