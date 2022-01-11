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
	"fmt"
	"os"
	"strconv"
	"strings"
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

type internalNode struct {
	inode Ino
	name  string
	attr  *Attr
}

var internalNodes = []*internalNode{
	{logInode, ".accesslog", &Attr{Mode: 0400}},
	{controlInode, ".control", &Attr{Mode: 0666}},
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

func collectMetrics() []byte {
	mfs, err := prometheus.DefaultGatherer.Gather()
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
			var name string = *mf.Name
			for _, l := range m.Label {
				if *l.Name != "mp" && *l.Name != "vol_name" {
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

func (v *VFS) handleInternalMsg(ctx Context, cmd uint32, r *utils.Buffer) []byte {
	switch cmd {
	case meta.Rmr:
		inode := Ino(r.Get64())
		name := string(r.Get(int(r.Get8())))
		r := meta.Remove(v.Meta, ctx, inode, name)
		return []byte{uint8(r)}
	case meta.Info:
		var summary meta.Summary
		inode := Ino(r.Get64())
		var recursive uint8 = 1
		if r.HasMore() {
			recursive = r.Get8()
		}

		wb := utils.NewBuffer(4)
		r := meta.GetSummary(v.Meta, ctx, inode, &summary, recursive != 0)
		if r != 0 {
			msg := r.Error()
			wb.Put32(uint32(len(msg)))
			return append(wb.Bytes(), []byte(msg)...)
		}
		var w = bytes.NewBuffer(nil)
		fmt.Fprintf(w, " inode: %d\n", inode)
		fmt.Fprintf(w, " files:\t%d\n", summary.Files)
		fmt.Fprintf(w, " dirs:\t%d\n", summary.Dirs)
		fmt.Fprintf(w, " length:\t%d\n", summary.Length)
		fmt.Fprintf(w, " size:\t%d\n", summary.Size)

		if summary.Files == 1 && summary.Dirs == 0 {
			fmt.Fprintf(w, " chunks:\n")
			for indx := uint64(0); indx*meta.ChunkSize < summary.Length; indx++ {
				var cs []meta.Slice
				_ = v.Meta.Read(ctx, inode, uint32(indx), &cs)
				for _, c := range cs {
					fmt.Fprintf(w, "\t%d:\t%d\t%d\t%d\t%d\n", indx, c.Chunkid, c.Size, c.Off, c.Len)
				}
			}
		}
		wb.Put32(uint32(w.Len()))
		return append(wb.Bytes(), w.Bytes()...)
	case meta.FillCache:
		paths := strings.Split(string(r.Get(int(r.Get32()))), "\n")
		concurrent := r.Get16()
		background := r.Get8()
		if background == 0 {
			v.fillCache(paths, int(concurrent))
		} else {
			go v.fillCache(paths, int(concurrent))
		}
		return []byte{uint8(0)}
	default:
		logger.Warnf("unknown message type: %d", cmd)
		return []byte{uint8(syscall.EINVAL & 0xff)}
	}
}
