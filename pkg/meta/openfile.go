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
	chunks    map[uint32][]Slice // for normal file
	children  []*Entry           // for directory
}

type openfiles struct {
	sync.Mutex
	expire time.Duration
	files  map[Ino]*openFile
}

func newOpenFiles(expire time.Duration) *openfiles {
	of := &openfiles{
		expire: expire,
		files:  make(map[Ino]*openFile),
	}
	go of.cleanup()
	return of
}

func (o *openfiles) cleanup() {
	for {
		o.Lock()
		cutoff := time.Now().Add(-time.Hour).Add(time.Second * time.Duration(len(o.files)/1e4))
		var cnt, expired int
		for ino, of := range o.files {
			if of.refs <= 0 && of.lastCheck.Before(cutoff) {
				delete(o.files, ino)
				expired++
			}
			cnt++
			if cnt > 1e3 {
				break
			}
		}
		o.Unlock()
		time.Sleep(time.Millisecond * time.Duration(1000*(cnt+1-expired*2)/(cnt+1)))
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
		if attr != nil && attr.Typ == TypeFile {
			of.chunks = make(map[uint32][]Slice)
		}
		o.files[ino] = of
	} else if of.shouldKeepCache(attr) {
		attr.KeepCache = of.attr.KeepCache
	} else {
		of.resetCache()
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
		if !of.shouldKeepCache(attr) {
			of.resetCache()
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

func (o *openfiles) InvalidateDir(ino Ino) {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok {
		of.children = nil
		of.lastCheck = time.Unix(0, 0)
	}
}

func (o *openfiles) find(ino Ino) *openFile {
	o.Lock()
	defer o.Unlock()
	return o.files[ino]
}

func (of *openFile) resetCache() {
	if of.attr.Typ == TypeFile {
		of.chunks = make(map[uint32][]Slice)
	} else if of.attr.Typ == TypeDirectory {
		of.children = nil
	}
}

func (of *openFile) shouldKeepCache(attr *Attr) bool {
	if attr != nil {
		same := attr.Mtime == of.attr.Mtime && attr.Mtimensec == of.attr.Mtimensec
		if !same {
			return false
		}
		if of.attr.Typ == TypeFile {
			// some client may not update mtime of directory when the time is out of sync
			if time.Since(time.Unix(attr.Mtime, int64(attr.Mtimensec))) < time.Minute {
				return false
			}
		}
		return true
	}
	return false
}
