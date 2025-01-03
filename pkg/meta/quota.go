/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/pkg/errors"
)

// stat of dir
type dirStat struct {
	length int64
	space  int64
	inodes int64
}

type Quota struct {
	MaxSpace, MaxInodes   int64
	UsedSpace, UsedInodes int64
	newSpace, newInodes   int64
}

// Returns true if it will exceed the quota limit
func (q *Quota) check(space, inodes int64) bool {
	if space > 0 {
		max := atomic.LoadInt64(&q.MaxSpace)
		if max > 0 && atomic.LoadInt64(&q.UsedSpace)+atomic.LoadInt64(&q.newSpace)+space > max {
			return true
		}
	}
	if inodes > 0 {
		max := atomic.LoadInt64(&q.MaxInodes)
		if max > 0 && atomic.LoadInt64(&q.UsedInodes)+atomic.LoadInt64(&q.newInodes)+inodes > max {
			return true
		}
	}
	return false
}

func (q *Quota) update(space, inodes int64) {
	atomic.AddInt64(&q.newSpace, space)
	atomic.AddInt64(&q.newInodes, inodes)
}

func (q *Quota) snap() Quota {
	return Quota{
		MaxSpace:   atomic.LoadInt64(&q.MaxSpace),
		MaxInodes:  atomic.LoadInt64(&q.MaxInodes),
		UsedSpace:  atomic.LoadInt64(&q.UsedSpace),
		UsedInodes: atomic.LoadInt64(&q.UsedInodes),
		newSpace:   atomic.LoadInt64(&q.newSpace),
		newInodes:  atomic.LoadInt64(&q.newInodes),
	}
}

// not thread safe
func (q *Quota) sanitize() {
	if q.UsedSpace < 0 {
		q.UsedSpace = 0
	}
	if q.MaxSpace > 0 && q.MaxSpace < q.UsedSpace {
		q.MaxSpace = q.UsedSpace
	}
	if q.UsedInodes < 0 {
		q.UsedInodes = 0
	}
	if q.MaxInodes > 0 && q.MaxInodes < q.UsedInodes {
		q.MaxInodes = q.UsedInodes
	}
}

func (m *baseMeta) parallelSyncDirStat(ctx Context, inos map[Ino]bool) *sync.WaitGroup {
	var wg sync.WaitGroup
	for i := range inos {
		wg.Add(1)
		go func(ino Ino) {
			defer wg.Done()
			_, st := m.en.doSyncDirStat(ctx, ino)
			if st != 0 && st != syscall.ENOENT {
				logger.Warnf("sync dir stat for %d: %s", ino, st)
			}
		}(i)
	}
	return &wg
}

func (m *baseMeta) groupBatch(batch map[Ino]dirStat, size int) [][]Ino {
	var inos []Ino
	for ino := range batch {
		inos = append(inos, ino)
	}
	sort.Slice(inos, func(i, j int) bool {
		return inos[i] < inos[j]
	})
	var batches [][]Ino
	for i := 0; i < len(inos); i += size {
		end := i + size
		if end > len(inos) {
			end = len(inos)
		}
		batches = append(batches, inos[i:end])
	}
	return batches
}

func (m *baseMeta) calcDirStat(ctx Context, ino Ino) (*dirStat, syscall.Errno) {
	var entries []*Entry
	if eno := m.en.doReaddir(ctx, ino, 1, &entries, -1); eno != 0 {
		return nil, eno
	}

	stat := new(dirStat)
	for _, e := range entries {
		stat.inodes += 1
		var l uint64
		if e.Attr.Typ == TypeFile {
			l = e.Attr.Length
		}
		stat.length += int64(l)
		stat.space += align4K(l)
	}
	return stat, 0
}

func (m *baseMeta) GetDirStat(ctx Context, inode Ino) (stat *dirStat, st syscall.Errno) {
	stat, st = m.en.doGetDirStat(ctx, m.checkRoot(inode), !m.conf.ReadOnly)
	if st != 0 {
		return
	}
	if stat == nil {
		stat, st = m.calcDirStat(ctx, inode)
	}
	return
}

func (m *baseMeta) updateDirStat(ctx Context, ino Ino, length, space, inodes int64) {
	if !m.getFormat().DirStats {
		return
	}
	m.dirStatsLock.Lock()
	defer m.dirStatsLock.Unlock()
	stat := m.dirStats[ino]
	stat.length += length
	stat.inodes += inodes
	stat.space += space
	m.dirStats[ino] = stat
}

func (m *baseMeta) updateParentStat(ctx Context, inode, parent Ino, length, space int64) {
	if length == 0 && space == 0 {
		return
	}
	m.en.updateStats(space, 0)
	if !m.getFormat().DirStats {
		return
	}
	if parent > 0 {
		m.updateDirStat(ctx, parent, length, space, 0)
		m.updateDirQuota(ctx, parent, space, 0)
	} else {
		go func() {
			for p, v := range m.en.doGetParents(ctx, inode) {
				m.updateDirStat(ctx, p, length*int64(v), space*int64(v), 0)
				m.updateDirQuota(ctx, p, space*int64(v), 0)
			}
		}()
	}
}

func (m *baseMeta) flushDirStat(ctx Context) {
	defer m.sessWG.Done()
	period := 1 * time.Second
	if m.conf.DirStatFlushPeriod != 0 {
		period = m.conf.DirStatFlushPeriod
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.doFlushDirStat()
		}
	}
}

func (m *baseMeta) doFlushDirStat() {
	if !m.getFormat().DirStats {
		return
	}
	m.dirStatsLock.Lock()
	if len(m.dirStats) == 0 {
		m.dirStatsLock.Unlock()
		return
	}
	stats := m.dirStats
	m.dirStats = make(map[Ino]dirStat)
	m.dirStatsLock.Unlock()
	err := m.en.doUpdateDirStat(Background(), stats)
	if err != nil {
		logger.Errorf("update dir stat failed: %v", err)
	}
}

func (m *baseMeta) flushStats(ctx Context) {
	defer m.sessWG.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.doFlushStats()
		}
	}
}

func (m *baseMeta) doFlushStats() {
	m.fsStatsLock.Lock()
	m.en.doFlushStats()
	m.fsStatsLock.Unlock()
}

func (m *baseMeta) syncUsedSpace(ctx Context) error {
	return m.en.doSyncUsedSpace(ctx)
}

func (m *baseMeta) checkQuota(ctx Context, space, inodes int64, parents ...Ino) syscall.Errno {
	if space <= 0 && inodes <= 0 {
		return 0
	}
	format := m.getFormat()
	if space > 0 && format.Capacity > 0 && atomic.LoadInt64(&m.usedSpace)+atomic.LoadInt64(&m.newSpace)+space > int64(format.Capacity) {
		return syscall.ENOSPC
	}
	if inodes > 0 && format.Inodes > 0 && atomic.LoadInt64(&m.usedInodes)+atomic.LoadInt64(&m.newInodes)+inodes > int64(format.Inodes) {
		return syscall.ENOSPC
	}
	if !format.DirStats {
		return 0
	}
	for _, ino := range parents {
		if m.checkDirQuota(ctx, ino, space, inodes) {
			return syscall.EDQUOT
		}
	}
	return 0
}

func (m *baseMeta) loadQuotas() {
	if !m.getFormat().DirStats {
		return
	}
	quotas, err := m.en.doLoadQuotas(Background())
	if err == nil {
		m.quotaMu.Lock()
		for ino := range m.dirQuotas {
			if _, ok := quotas[ino]; !ok {
				logger.Infof("Quota for inode %d is deleted", ino)
				delete(m.dirQuotas, ino)
			}
		}
		for ino, q := range quotas {
			logger.Debugf("Load quotas got %d -> %+v", ino, q)
			if _, ok := m.dirQuotas[ino]; !ok {
				m.dirQuotas[ino] = q
			}
		}
		m.quotaMu.Unlock()

		// skip lock since I'm the only one updating the m.dirQuotas
		for ino, q := range quotas {
			quota := m.dirQuotas[ino]
			atomic.SwapInt64(&quota.MaxSpace, q.MaxSpace)
			atomic.SwapInt64(&quota.MaxInodes, q.MaxInodes)
			atomic.SwapInt64(&quota.UsedSpace, q.UsedSpace)
			atomic.SwapInt64(&quota.UsedInodes, q.UsedInodes)
		}
	} else {
		logger.Warnf("Load quotas: %s", err)
	}
}

func (m *baseMeta) getDirParent(ctx Context, inode Ino) (Ino, syscall.Errno) {
	m.parentMu.Lock()
	parent, ok := m.dirParents[inode]
	m.parentMu.Unlock()
	if ok {
		return parent, 0
	}
	logger.Debugf("Get directory parent of inode %d: cache miss", inode)
	var attr Attr
	st := m.GetAttr(ctx, inode, &attr)
	return attr.Parent, st
}

// get inode of the first parent (or myself) with quota
func (m *baseMeta) getQuotaParent(ctx Context, inode Ino) (Ino, *Quota) {
	if !m.getFormat().DirStats {
		return 0, nil
	}
	var q *Quota
	var st syscall.Errno
	for {
		m.quotaMu.RLock()
		q = m.dirQuotas[inode]
		m.quotaMu.RUnlock()
		if q != nil {
			return inode, q
		}
		if inode <= RootInode {
			break
		}
		lastInode := inode
		if inode, st = m.getDirParent(ctx, inode); st != 0 {
			logger.Warnf("Get directory parent of inode %d: %s", lastInode, st)
			break
		}
	}
	return 0, nil
}

func (m *baseMeta) checkDirQuota(ctx Context, inode Ino, space, inodes int64) bool {
	if !m.getFormat().DirStats {
		return false
	}
	var q *Quota
	var st syscall.Errno
	for {
		m.quotaMu.RLock()
		q = m.dirQuotas[inode]
		m.quotaMu.RUnlock()
		if q != nil && q.check(space, inodes) {
			return true
		}
		if inode <= RootInode {
			break
		}
		lastInode := inode
		if inode, st = m.getDirParent(ctx, inode); st != 0 {
			logger.Warnf("Get directory parent of inode %d: %s", lastInode, st)
			break
		}
	}
	return false
}

func (m *baseMeta) updateDirQuota(ctx Context, inode Ino, space, inodes int64) {
	if !m.getFormat().DirStats {
		return
	}
	var q *Quota
	var st syscall.Errno
	for {
		m.quotaMu.RLock()
		q = m.dirQuotas[inode]
		m.quotaMu.RUnlock()
		if q != nil {
			q.update(space, inodes)
		}
		if inode <= RootInode {
			break
		}
		lastInode := inode
		if inode, st = m.getDirParent(ctx, inode); st != 0 {
			logger.Warnf("Get directory parent of inode %d: %s", lastInode, st)
			break
		}
	}
}

func (m *baseMeta) flushQuotas(ctx Context) {
	defer m.sessWG.Done()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.doFlushQuotas()
		}
	}
}

func (m *baseMeta) doFlushQuotas() {
	if !m.getFormat().DirStats {
		return
	}
	stageMap := make(map[Ino]*Quota)
	m.quotaMu.RLock()
	for ino, q := range m.dirQuotas {
		newSpace := atomic.LoadInt64(&q.newSpace)
		newInodes := atomic.LoadInt64(&q.newInodes)
		if newSpace != 0 || newInodes != 0 {
			stageMap[ino] = &Quota{newSpace: newSpace, newInodes: newInodes}
		}
	}
	m.quotaMu.RUnlock()
	if len(stageMap) == 0 {
		return
	}

	if err := m.en.doFlushQuotas(Background(), stageMap); err != nil {
		logger.Warnf("Flush quotas: %s", err)
	} else {
		m.quotaMu.RLock()
		for ino, snap := range stageMap {
			q := m.dirQuotas[ino]
			if q == nil {
				continue
			}
			atomic.AddInt64(&q.newSpace, -snap.newSpace)
			atomic.AddInt64(&q.UsedSpace, snap.newSpace)
			atomic.AddInt64(&q.newInodes, -snap.newInodes)
			atomic.AddInt64(&q.UsedInodes, snap.newInodes)
		}
		m.quotaMu.RUnlock()
	}
}

func (m *baseMeta) HandleQuota(ctx Context, cmd uint8, dpath string, quotas map[string]*Quota, strict, repair bool, create bool) error {
	var inode Ino
	if cmd != QuotaList {
		if st := m.resolve(ctx, dpath, &inode, create); st != 0 {
			return fmt.Errorf("resolve dir %s: %s", dpath, st)
		}
		if isTrash(inode) {
			return errors.New("no quota for any trash directory")
		}
	}

	switch cmd {
	case QuotaSet:
		format := m.getFormat()
		if !format.DirStats {
			format.DirStats = true
			if err := m.en.doInit(format, false); err != nil {
				return err
			}
		}
		quota := quotas[dpath]
		created, err := m.en.doSetQuota(ctx, inode, &Quota{
			MaxSpace:   quota.MaxSpace,
			MaxInodes:  quota.MaxInodes,
			UsedSpace:  -1,
			UsedInodes: -1,
		})
		if err != nil {
			return err
		}
		if created {
			wrapErr := func(e error) error {
				return errors.Wrapf(e, "set quota usage for file(%s), please repair it later", dpath)
			}
			var sum Summary
			if st := m.GetSummary(ctx, inode, &sum, true, strict); st != 0 {
				return wrapErr(st)
			}
			_, err := m.en.doSetQuota(ctx, inode, &Quota{
				UsedSpace:  int64(sum.Size) - align4K(0),
				UsedInodes: int64(sum.Dirs+sum.Files) - 1,
				MaxSpace:   -1,
				MaxInodes:  -1,
			})
			if err != nil {
				return wrapErr(err)
			}
			return nil
		}
	case QuotaGet:
		q, err := m.en.doGetQuota(ctx, inode)
		if err != nil {
			return err
		}
		if q == nil {
			return fmt.Errorf("no quota for inode %d path %s", inode, dpath)
		}
		quotas[dpath] = q
	case QuotaDel:
		return m.en.doDelQuota(ctx, inode)
	case QuotaList:
		quotaMap, err := m.en.doLoadQuotas(ctx)
		if err != nil {
			return err
		}
		var p string
		for ino, quota := range quotaMap {
			if ps := m.GetPaths(ctx, ino); len(ps) > 0 {
				p = ps[0]
			} else {
				p = fmt.Sprintf("inode:%d", ino)
			}
			quotas[p] = quota
		}
	case QuotaCheck:
		q, err := m.en.doGetQuota(ctx, inode)
		if err != nil {
			return err
		}
		if q == nil {
			return fmt.Errorf("no quota for inode %d path %s", inode, dpath)
		}
		var sum Summary
		if st := m.GetSummary(ctx, inode, &sum, true, strict); st != 0 {
			return st
		}
		usedInodes := int64(sum.Dirs+sum.Files) - 1
		usedSpace := int64(sum.Size) - align4K(0) // quota ignore root dir
		if q.UsedInodes == usedInodes && q.UsedSpace == usedSpace {
			logger.Infof("quota of %s is consistent", dpath)
			quotas[dpath] = q
			return nil
		}
		logger.Warnf(
			"%s: quota(%s, %s) != summary(%s, %s)", dpath,
			humanize.Comma(q.UsedInodes), humanize.IBytes(uint64(q.UsedSpace)),
			humanize.Comma(usedInodes), humanize.IBytes(uint64(usedSpace)),
		)
		if repair {
			q.UsedInodes = usedInodes
			q.UsedSpace = usedSpace
			quotas[dpath] = q
			logger.Info("repairing...")
			_, err = m.en.doSetQuota(ctx, inode, &Quota{
				MaxInodes:  -1,
				MaxSpace:   -1,
				UsedInodes: q.UsedInodes,
				UsedSpace:  q.UsedSpace,
			})
			return err
		}
		return fmt.Errorf("quota of %s is inconsistent, please repair it with --repair flag", dpath)
	default:
		return fmt.Errorf("invalid quota command: %d", cmd)
	}
	return nil
}
