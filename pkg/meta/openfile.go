package meta

import (
	"sync"
	"time"
)

type openFile struct {
	attr      Attr
	refs      int
	lastCheck time.Time
	chunks    map[uint32][]Slice
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
		cutoff := time.Now().Add(-time.Hour)
		for ino, of := range o.files {
			if of.refs <= 0 && of.lastCheck.Before(cutoff) {
				delete(o.files, ino)
			}
		}
		o.Unlock()
		time.Sleep(time.Second)
	}
}

func (o *openfiles) OpenCheck(ino Ino, attr *Attr) bool {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok && time.Since(of.lastCheck) < o.expire {
		if attr != nil {
			*attr = of.attr
			attr.KeepCache = true
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
		attr.KeepCache = true
	} else {
		of.chunks = make(map[uint32][]Slice)
	}
	if attr != nil {
		of.attr = *attr
	}
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
		if indx == 0xFFFFFFFF {
			of.chunks = make(map[uint32][]Slice)
		} else {
			delete(of.chunks, indx)
		}
		of.lastCheck = time.Unix(0, 0)
	}
}
