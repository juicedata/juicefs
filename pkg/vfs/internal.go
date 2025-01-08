/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package vfs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
)

const (
	minInternalNode = 0x7FFFFFFF00000000
	logInode        = minInternalNode + 1
	controlInode    = minInternalNode + 2
	statsInode      = minInternalNode + 3
	configInode     = minInternalNode + 4
	trashInode      = meta.TrashInode
)

var controlMutex sync.Mutex
var controlHandlers = make(map[uint32]uint64)

func (v *VFS) getControlHandle(pid uint32) uint64 {
	controlMutex.Lock()
	defer controlMutex.Unlock()
	fh := controlHandlers[pid]
	if fh == 0 {
		h := v.newHandle(controlInode, false)
		fh = h.fh
		controlHandlers[pid] = fh
	}
	return fh
}

func (v *VFS) releaseControlHandle(pid uint32) {
	controlMutex.Lock()
	defer controlMutex.Unlock()
	fh := controlHandlers[pid]
	if fh != 0 {
		v.releaseHandle(controlInode, fh)
		delete(controlHandlers, pid)
	}
}

type internalNode struct {
	inode Ino
	name  string
	attr  *Attr
}

var internalNodes = []*internalNode{
	{controlInode, ".control", &Attr{Mode: 0666}},
	{logInode, ".accesslog", &Attr{Mode: 0400}},
	{statsInode, ".stats", &Attr{Mode: 0444}},
	{configInode, ".config", &Attr{Mode: 0400}},
	{trashInode, meta.TrashName, &Attr{Mode: 0555}},
}

func init() {
	uid := uint32(os.Getuid())
	gid := uint32(os.Getgid())
	now := time.Now().Unix()
	for _, v := range internalNodes {
		if v.inode == trashInode {
			v.attr.Typ = meta.TypeDirectory
			v.attr.Nlink = 2
		} else {
			v.attr.Typ = meta.TypeFile
			v.attr.Nlink = 1
			v.attr.Uid = uid
			v.attr.Gid = gid
		}
		v.attr.Atime = now
		v.attr.Mtime = now
		v.attr.Ctime = now
		v.attr.Full = true
	}
}

func IsSpecialNode(ino Ino) bool {
	return ino >= minInternalNode
}

func IsSpecialName(name string) bool {
	if name[0] != '.' {
		return false
	}
	for _, n := range internalNodes {
		if name == n.name {
			return true
		}
	}
	return false
}

func getInternalNode(ino Ino) *internalNode {
	for _, n := range internalNodes {
		if ino == n.inode {
			return n
		}
	}
	return nil
}

func GetInternalNodeByName(name string) (Ino, *Attr) {
	n := getInternalNodeByName(name)
	if n != nil {
		return n.inode, n.attr
	}
	return 0, nil
}

func getInternalNodeByName(name string) *internalNode {
	if name[0] != '.' {
		return nil
	}
	for _, n := range internalNodes {
		if name == n.name {
			return n
		}
	}
	return nil
}

func collectMetrics(registry *prometheus.Registry) []byte {
	if registry == nil {
		return []byte("")
	}
	mfs, err := registry.Gather()
	if err != nil {
		logger.Errorf("collect metrics: %s", err)
		return nil
	}
	w := bytes.NewBuffer(nil)
	format := func(v float64) string {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	for _, mf := range mfs {
		for _, m := range mf.Metric {
			var name = *mf.Name
			for _, l := range m.Label {
				if *l.Name == "method" || *l.Name == "errno" {
					name += "_" + *l.Value
				}
			}
			switch *mf.Type {
			case io_prometheus_client.MetricType_GAUGE:
				_, _ = fmt.Fprintf(w, "%s %s\n", name, format(*m.Gauge.Value))
			case io_prometheus_client.MetricType_COUNTER:
				_, _ = fmt.Fprintf(w, "%s %s\n", name, format(*m.Counter.Value))
			case io_prometheus_client.MetricType_HISTOGRAM:
				_, _ = fmt.Fprintf(w, "%s_total %d\n", name, *m.Histogram.SampleCount)
				_, _ = fmt.Fprintf(w, "%s_sum %s\n", name, format(*m.Histogram.SampleSum))
			case io_prometheus_client.MetricType_SUMMARY:
			}
		}
	}
	return w.Bytes()
}

func writeProgress(item1, item2 *uint64, out io.Writer, done chan struct{}) {
	wb := utils.NewBuffer(17)
	wb.Put8(meta.CPROGRESS)
	if item2 == nil {
		item2 = new(uint64)
	}
	ticker := time.NewTicker(time.Millisecond * 300)
	for {
		select {
		case <-ticker.C:
			wb.Put64(atomic.LoadUint64(item1))
			wb.Put64(atomic.LoadUint64(item2))
			_, _ = out.Write(wb.Bytes())
			wb.Seek(1)
		case <-done:
			ticker.Stop()
			if *item1 > 0 || *item2 > 0 {
				wb.Put64(atomic.LoadUint64(item1))
				wb.Put64(atomic.LoadUint64(item2))
				_, _ = out.Write(wb.Bytes())
			}
			return
		}
	}
}

type obj struct {
	key            string
	size, off, len uint32
}

func (v *VFS) caclObjects(id uint64, size, offset, length uint32) []*obj {
	if id == 0 {
		return []*obj{{"", size, offset, length}}
	}
	if length == 0 || offset+length > size {
		logger.Warnf("Corrupt slice id %d size %d offset %d length %d", id, size, offset, length)
		return nil
	}
	bsize := uint32(v.Conf.Chunk.BlockSize)
	var prefix string
	if v.Conf.Chunk.HashPrefix {
		prefix = fmt.Sprintf("%s/chunks/%02X/%v/%v", v.Conf.Format.Name, id%256, id/1000/1000, id)
	} else {
		prefix = fmt.Sprintf("%s/chunks/%v/%v/%v", v.Conf.Format.Name, id/1000/1000, id/1000, id)
	}
	first := offset / bsize
	last := (offset + length - 1) / bsize
	objs := make([]*obj, 0, last-first+1)
	for indx := first; indx <= last; indx++ {
		objs = append(objs, &obj{fmt.Sprintf("%s_%d_%d", prefix, indx, bsize), bsize, 0, bsize})
	}
	fo, lo := objs[0], objs[len(objs)-1]
	fo.off = offset - first*bsize
	fo.len = fo.size - fo.off
	if (last+1)*bsize > size {
		lo.size = size - last*bsize
		lo.key = fmt.Sprintf("%s_%d_%d", prefix, last, lo.size)
	}
	lo.len = (offset + length) - last*bsize - lo.off

	return objs
}

type InfoResponse struct {
	Ino     Ino
	Failed  bool
	Reason  string
	Summary meta.Summary
	Paths   []string
	Chunks  []*chunkSlice
	Objects []*chunkObj
	PLocks  []meta.PLockItem
	FLocks  []meta.FLockItem
}

type SummaryReponse struct {
	Errno syscall.Errno
	Tree  meta.TreeSummary
}

type CacheResponse struct {
	FileCount  uint64
	SliceCount uint64
	TotalBytes uint64
	MissBytes  uint64 // for check op
}

func (resp *CacheResponse) Add(other CacheResponse) {
	resp.FileCount += other.FileCount
	resp.TotalBytes += other.TotalBytes
	resp.SliceCount += other.SliceCount
	resp.MissBytes += other.MissBytes
}

type chunkSlice struct {
	ChunkIndex uint64
	meta.Slice
}

type chunkObj struct {
	ChunkIndex     uint64
	Key            string
	Size, Off, Len uint32
}

func (v *VFS) handleInternalMsg(ctx meta.Context, cmd uint32, r *utils.Buffer, out io.Writer) {
	switch cmd {
	case meta.Rmr:
		done := make(chan struct{})
		inode := Ino(r.Get64())
		name := string(r.Get(int(r.Get8())))
		var skipTrash bool
		var numThreads int = meta.RmrDefaultThreads
		if r.HasMore() {
			skipTrash = r.Get8()&1 != 0
		}
		if r.HasMore() {
			numThreads = int(r.Get8())
		}
		var count uint64
		var st syscall.Errno
		go func() {
			st = v.Meta.Remove(ctx, inode, name, skipTrash, numThreads, &count)
			if st != 0 {
				logger.Errorf("remove %d/%s: %s", inode, name, st)
			}
			close(done)
		}()
		writeProgress(&count, nil, out, done)
		if st == 0 && v.InvalidateEntry != nil {
			if st := v.InvalidateEntry(inode, name); st != 0 {
				logger.Warnf("Invalidate entry %d/%s: %s", inode, name, st)
			}
		}
		_, _ = out.Write([]byte{uint8(st)})
	case meta.Clone:
		done := make(chan struct{})
		srcIno := Ino(r.Get64())
		srcParentIno := Ino(r.Get64())
		dstParentIno := Ino(r.Get64())
		dstName := string(r.Get(int(r.Get8())))
		umask := r.Get16()
		cmode := r.Get8()
		var count, total uint64
		var eno syscall.Errno
		go func() {
			if eno = v.Meta.Clone(ctx, srcParentIno, srcIno, dstParentIno, dstName, cmode, umask, &count, &total); eno != 0 {
				logger.Errorf("clone failed srcIno:%d,dstParentIno:%d,dstName:%s,cmode:%d,umask:%d,eno:%v", srcIno, dstParentIno, dstName, cmode, umask, eno)
			}
			close(done)
		}()

		writeProgress(&count, &total, out, done)
		_, _ = out.Write([]byte{uint8(eno)})

	case meta.LegacyInfo:
		var summary meta.Summary
		inode := Ino(r.Get64())
		var recursive uint8 = 1
		if r.HasMore() {
			recursive = r.Get8()
		}
		var raw bool
		if r.HasMore() {
			raw = r.Get8() != 0
		}

		wb := utils.NewBuffer(4)
		r := v.Meta.GetSummary(ctx, inode, &summary, recursive != 0, true)
		if r != 0 {
			msg := r.Error()
			wb.Put32(uint32(len(msg)))
			_, _ = out.Write(append(wb.Bytes(), msg...))
			return
		}
		var w = bytes.NewBuffer(nil)
		fmt.Fprintf(w, "  inode: %d\n", inode)
		fmt.Fprintf(w, "  files: %d\n", summary.Files)
		fmt.Fprintf(w, "   dirs: %d\n", summary.Dirs)
		fmt.Fprintf(w, " length: %s\n", utils.FormatBytes(summary.Length))
		fmt.Fprintf(w, "   size: %s\n", utils.FormatBytes(summary.Size))
		ps := v.Meta.GetPaths(ctx, inode)
		switch len(ps) {
		case 0:
			fmt.Fprintf(w, "   path: %s\n", "unknown")
		case 1:
			fmt.Fprintf(w, "   path: %s\n", ps[0])
		default:
			fmt.Fprintf(w, "  paths:\n")
			for _, p := range ps {
				fmt.Fprintf(w, "\t%s\n", p)
			}
		}
		if summary.Files == 1 && summary.Dirs == 0 {
			if raw {
				fmt.Fprintf(w, " chunks:\n")
			} else {
				fmt.Fprintf(w, "objects:\n")
			}
			for indx := uint64(0); indx*meta.ChunkSize < summary.Length; indx++ {
				var cs []meta.Slice
				_ = v.Meta.Read(ctx, inode, uint32(indx), &cs)
				for _, c := range cs {
					if raw {
						fmt.Fprintf(w, "\t%d:\t%d\t%d\t%d\t%d\n", indx, c.Id, c.Size, c.Off, c.Len)
					} else {
						for _, o := range v.caclObjects(c.Id, c.Size, c.Off, c.Len) {
							fmt.Fprintf(w, "\t%d:\t%s\t%d\t%d\t%d\n", indx, o.key, o.size, o.off, o.len)
						}
					}
				}
			}
		}
		wb.Put32(uint32(w.Len()))
		_, _ = out.Write(append(wb.Bytes(), w.Bytes()...))
	case meta.InfoV2:
		inode := Ino(r.Get64())
		info := &InfoResponse{
			Ino: inode,
		}

		var recursive uint8 = 1
		if r.HasMore() {
			recursive = r.Get8()
		}
		var raw bool
		if r.HasMore() {
			raw = r.Get8() != 0
		}
		var strict bool
		if r.HasMore() {
			strict = r.Get8() != 0
		}

		done := make(chan struct{})
		var r syscall.Errno
		go func() {
			r = v.Meta.GetSummary(ctx, inode, &info.Summary, recursive != 0, strict)
			close(done)
		}()
		writeProgress(&info.Summary.Files, &info.Summary.Size, out, done)
		if r != 0 {
			info.Failed = true
			info.Reason = r.Error()
		} else {
			info.Paths = v.Meta.GetPaths(ctx, inode)
			if info.Summary.Files == 1 && info.Summary.Dirs == 0 {
				for indx := uint64(0); indx*meta.ChunkSize < info.Summary.Length; indx++ {
					var cs []meta.Slice
					_ = v.Meta.Read(ctx, inode, uint32(indx), &cs)
					for _, c := range cs {
						if raw {
							info.Chunks = append(info.Chunks, &chunkSlice{indx, c})
						} else {
							for _, o := range v.caclObjects(c.Id, c.Size, c.Off, c.Len) {
								info.Objects = append(info.Objects, &chunkObj{indx, o.key, o.size, o.off, o.len})
							}
						}
					}
				}
			}

			var err error
			if info.PLocks, info.FLocks, err = v.Meta.ListLocks(ctx, inode); err != nil {
				info.Failed = true
				info.Reason = err.Error()
			}
		}
		data, err := json.Marshal(info)
		if err != nil {
			logger.Errorf("marshal info response: %v", err)
			_, _ = out.Write([]byte{byte(syscall.EIO & 0xff)})
			return
		}
		w := utils.NewBuffer(uint32(1 + 4 + len(data)))
		w.Put8(meta.CDATA)
		w.Put32(uint32(len(data)))
		w.Put(data)
		_, _ = out.Write(w.Bytes())
	case meta.OpSummary:
		inode := Ino(r.Get64())
		tree := meta.TreeSummary{
			Inode: inode,
			Path:  "",
			Type:  meta.TypeDirectory,
		}

		var depth uint8 = 3
		if r.HasMore() {
			depth = r.Get8()
		}
		var topN uint8 = 10
		if r.HasMore() {
			topN = r.Get8()
		}
		var strict bool
		if r.HasMore() {
			strict = r.Get8() != 0
		}

		done := make(chan struct{})
		var files, size uint64
		var r syscall.Errno
		go func() {
			r = v.Meta.GetTreeSummary(ctx, &tree, depth, topN, strict,
				func(count, bytes uint64) {
					atomic.AddUint64(&files, count)
					atomic.AddUint64(&size, bytes)
				})
			close(done)
		}()
		writeProgress(&files, &size, out, done)
		data, err := json.Marshal(&SummaryReponse{r, tree})
		if err != nil {
			logger.Errorf("marshal summary response: %v", err)
			_, _ = out.Write([]byte{byte(syscall.EIO & 0xff)})
			return
		}
		w := utils.NewBuffer(uint32(1 + 4 + len(data)))
		w.Put8(meta.CDATA)
		w.Put32(uint32(len(data)))
		w.Put(data)
		_, _ = out.Write(w.Bytes())
	case meta.CompactPath:
		inode := Ino(r.Get64())
		coCnt := r.Get16()

		done := make(chan struct{})
		var totalChunks, currChunks uint64
		var eno syscall.Errno
		go func() {
			eno = v.Meta.Compact(ctx, inode, int(coCnt), func() {
				atomic.AddUint64(&totalChunks, 1)
			}, func() {
				atomic.AddUint64(&currChunks, 1)
			})
			close(done)
		}()

		writeProgress(&totalChunks, &currChunks, out, done)
		_, _ = out.Write([]byte{uint8(eno)})

	case meta.FillCache:
		paths := strings.Split(string(r.Get(int(r.Get32()))), "\n")
		concurrent := r.Get16()
		background := r.Get8()

		action := WarmupCache
		if r.HasMore() {
			action = CacheAction(r.Get8())
		}

		var stat CacheResponse
		if background == 0 {
			done := make(chan struct{})
			go func() {
				v.cache(ctx, action, paths, int(concurrent), &stat)
				close(done)
			}()
			writeProgress(&stat.FileCount, &stat.TotalBytes, out, done)
		} else {
			go v.cache(meta.NewContext(ctx.Pid(), ctx.Uid(), ctx.Gids()), action, paths, int(concurrent), nil)
		}

		data, err := json.Marshal(stat)
		if err != nil {
			logger.Errorf("marshal response error: %v", err)
			_, _ = out.Write([]byte{byte(syscall.EIO & 0xff)})
			return
		}
		w := utils.NewBuffer(uint32(1 + 4 + len(data)))
		w.Put8(meta.CDATA)
		w.Put32(uint32(len(data)))
		w.Put(data)
		_, _ = out.Write(w.Bytes())
	default:
		logger.Warnf("unknown message type: %d", cmd)
		_, _ = out.Write([]byte{byte(syscall.EINVAL & 0xff)})
	}
}
