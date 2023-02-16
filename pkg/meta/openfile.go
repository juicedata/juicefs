package meta

import (
	"sync"
	"time"
)

const (
	invalidateAllChunks = 0xFFFFFFFF
	invalidateAttrOnly  = 0xFFFFFFFE
)

type openFile struct {
	sync.RWMutex
	attr      Attr
	refs      int
	lastCheck time.Time
	chunks    map[uint32][]Slice
}

type openfiles struct {
	sync.Mutex
	expire  time.Duration
	limit   uint64
	timeout time.Duration
	files   map[Ino]*openFile
}

func newOpenFiles(expire time.Duration, limit uint64, timeout time.Duration) *openfiles {
	of := &openfiles{
		expire:  expire,
		limit:   limit,
		timeout: timeout,
		files:   make(map[Ino]*openFile),
	}
	go of.cleanup()
	return of
}

func (o *openfiles) cleanup() {
	limit := uint64(1e4)
	timeout := time.Hour
	if o.limit > 0 {
		limit = o.limit
	}
	if o.timeout > 0 {
		timeout = o.timeout
	}

	for {
		expiredFiles := make(map[Ino]struct{})
		o.Lock()
		var cnt, deleted int
		for ino, of := range o.files {
			cnt++
			if of.refs <= 0 {
				if time.Since(of.lastCheck) > timeout {
					delete(o.files, ino)
					deleted++
					continue
				}
				expiredFiles[ino] = struct{}{}
			}
			if deleted > 1e3 {
				break
			}
		}
		for len(expiredFiles) > 0 && uint64(len(o.files)) > limit && deleted <= 1e3 {
			o.randomExpire(expiredFiles)
			deleted++
		}

		o.Unlock()
		time.Sleep(time.Millisecond * time.Duration(1000*(cnt+1-deleted*2)/(cnt+1)))
	}
}

func (o *openfiles) randomExpire(expiredFiles map[Ino]struct{}) {
	var random Ino
	for ino := range expiredFiles {
		random = ino
		break
	}
	defer func() {
		delete(o.files, random)
		delete(expiredFiles, random)
	}()

	if len(expiredFiles) == 1 {
		return
	}

	for ino := range expiredFiles {
		if ino == random {
			continue
		}
		if o.files[ino].lastCheck.Before(o.files[random].lastCheck) {
			random = ino
		}
		return
	}
}

func (o *openfiles) OpenCheck(ino Ino, attr *Attr) bool {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok && time.Since(of.lastCheck) < o.expire {
		if attr != nil {
			*attr = of.attr
		}
		of.refs++
		return true
	}
	return false
}

func (o *openfiles) Open(ino Ino, attr *Attr) {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if !ok {
		of = &openFile{}
		of.chunks = make(map[uint32][]Slice)
		o.files[ino] = of
	} else if attr != nil && attr.Mtime == of.attr.Mtime && attr.Mtimensec == of.attr.Mtimensec {
		attr.KeepCache = of.attr.KeepCache
	} else {
		of.chunks = make(map[uint32][]Slice)
	}
	if attr != nil {
		of.attr = *attr
	}
	// next open can keep cache if not modified
	of.attr.KeepCache = true
	of.refs++
	of.lastCheck = time.Now()
}

func (o *openfiles) Close(ino Ino) bool {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok {
		of.refs--
		return of.refs <= 0
	}
	return true
}

func (o *openfiles) Check(ino Ino, attr *Attr) bool {
	if attr == nil {
		panic("attr is nil")
	}
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok && time.Since(of.lastCheck) < o.expire {
		*attr = of.attr
		return true
	}
	return false
}

func (o *openfiles) Update(ino Ino, attr *Attr) bool {
	if attr == nil {
		panic("attr is nil")
	}
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok {
		if attr.Mtime != of.attr.Mtime || attr.Mtimensec != of.attr.Mtimensec {
			of.chunks = make(map[uint32][]Slice)
		} else {
			attr.KeepCache = of.attr.KeepCache
		}
		of.attr = *attr
		of.lastCheck = time.Now()
		return true
	}
	return false
}

func (o *openfiles) IsOpen(ino Ino) bool {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	return ok && of.refs > 0
}

func (o *openfiles) ReadChunk(ino Ino, indx uint32) ([]Slice, bool) {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if !ok {
		return nil, false
	}
	cs, ok := of.chunks[indx]
	return cs, ok
}

func (o *openfiles) CacheChunk(ino Ino, indx uint32, cs []Slice) {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok {
		of.chunks[indx] = cs
	}
}

func (o *openfiles) InvalidateChunk(ino Ino, indx uint32) {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok {
		if indx == invalidateAllChunks {
			of.chunks = make(map[uint32][]Slice)
		} else {
			delete(of.chunks, indx)
		}
		of.lastCheck = time.Unix(0, 0)
	}
}

func (o *openfiles) find(ino Ino) *openFile {
	o.Lock()
	defer o.Unlock()
	return o.files[ino]
}
