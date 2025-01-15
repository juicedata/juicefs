package meta

import (
	"sync"
	"time"
)

const (
	invalidateAllChunks = 0xFFFFFFFF
	invalidateAttrOnly  = 0xFFFFFFFE
)

var ofPool = sync.Pool{
	New: func() interface{} {
		return &openFile{}
	},
}

type openFile struct {
	sync.RWMutex
	attr      Attr
	refs      int
	lastCheck int64
	first     []Slice
	chunks    map[uint32][]Slice
}

func (o *openFile) invalidateChunk() {
	o.first = nil
	for c := range o.chunks {
		delete(o.chunks, c)
	}
}

func (o *openFile) release() {
	o.attr = Attr{}
	o.refs = 0
	o.lastCheck = 0
	o.first = nil
	o.chunks = nil
	ofPool.Put(o)
}

type openfiles struct {
	sync.Mutex
	expire time.Duration
	limit  uint64
	files  map[Ino]*openFile
}

func newOpenFiles(expire time.Duration, limit uint64) *openfiles {
	of := &openfiles{
		expire: expire,
		limit:  limit,
		files:  make(map[Ino]*openFile),
	}
	go of.cleanup()
	return of
}

func (o *openfiles) cleanup() {
	for {
		var (
			cnt, deleted, todel int
			candidateIno        Ino
			candidateOf         *openFile
		)
		o.Lock()
		if o.limit > 0 && len(o.files) > int(o.limit) {
			todel = len(o.files) - int(o.limit)
		}
		now := time.Now().Unix()
		for ino, of := range o.files {
			cnt++
			if cnt > 1e3 || todel > 0 && deleted >= todel {
				break
			}
			if of.refs <= 0 {
				if now-of.lastCheck > 3600*12 {
					of.release()
					delete(o.files, ino)
					deleted++
					continue
				}
				if todel == 0 {
					continue
				}
				if candidateIno == 0 {
					candidateIno = ino
					candidateOf = of
					continue
				}
				if of.lastCheck < candidateOf.lastCheck {
					candidateIno = ino
					candidateOf = of
				}
				candidateOf.release()
				delete(o.files, candidateIno)
				deleted++
				candidateIno = 0
			}
		}
		o.Unlock()
		time.Sleep(time.Millisecond * time.Duration(1000*(cnt+1-deleted*2)/(cnt+1)))
	}
}

func (o *openfiles) OpenCheck(ino Ino, attr *Attr) bool {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok && time.Second*time.Duration(time.Now().Unix()-of.lastCheck) < o.expire {
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
		of = ofPool.Get().(*openFile)
		o.files[ino] = of
	} else if attr != nil && attr.Mtime == of.attr.Mtime && attr.Mtimensec == of.attr.Mtimensec {
		attr.KeepCache = of.attr.KeepCache
	} else {
		of.invalidateChunk()
	}
	if attr != nil {
		of.attr = *attr
	}
	// next open can keep cache if not modified
	of.attr.KeepCache = true
	of.refs++
	of.lastCheck = time.Now().Unix()
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
	if ok && time.Second*time.Duration(time.Now().Unix()-of.lastCheck) < o.expire {
		*attr = of.attr
		return true
	}
	return false
}

func (o *openfiles) Update(ino Ino, attr *Attr) bool {
	if attr == nil {
		return false
	}
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok {
		if attr.Mtime != of.attr.Mtime || attr.Mtimensec != of.attr.Mtimensec {
			of.invalidateChunk()
		} else {
			attr.KeepCache = of.attr.KeepCache
		}
		of.attr = *attr
		of.lastCheck = time.Now().Unix()
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
	if indx == 0 {
		return of.first, of.first != nil
	} else {
		cs, ok := of.chunks[indx]
		return cs, ok
	}
}

func (o *openfiles) CacheChunk(ino Ino, indx uint32, cs []Slice) {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if !ok {
		return
	}
	if indx == 0 {
		of.first = cs
	} else {
		if of.chunks == nil {
			of.chunks = make(map[uint32][]Slice)
		}
		of.chunks[indx] = cs
	}
}

func (o *openfiles) InvalidateChunk(ino Ino, indx uint32) {
	o.Lock()
	defer o.Unlock()
	of, ok := o.files[ino]
	if ok {
		if indx == invalidateAllChunks {
			of.invalidateChunk()
		} else if indx == 0 {
			of.first = nil
		} else {
			delete(of.chunks, indx)
		}
		of.lastCheck = 0
	}
}

func (o *openfiles) find(ino Ino) *openFile {
	o.Lock()
	defer o.Unlock()
	return o.files[ino]
}
