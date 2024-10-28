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

package utils

import (
	"fmt"
	"os"
	"syscall"
)

var maxPipeSize int
var NullFD uintptr

type FdData struct {
	Fd   uintptr
	Off  int64
	Size int
}

const F_SETPIPE_SZ = 1031
const F_GETPIPE_SZ = 1032

// copy & paste from syscall.
func fcntl(fd uintptr, cmd int, arg int) (int, syscall.Errno) {
	r0, _, e1 := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(cmd), uintptr(arg))
	return int(r0), e1
}

// Copied from go-fuse
func TrySplice() bool {
	content, err := os.ReadFile("/proc/sys/fs/pipe-max-size")
	if err != nil {
		maxPipeSize = 16 << 10 // defaultPipeSize
	} else {
		fmt.Sscan(string(content), &maxPipeSize)
	}
	if maxPipeSize < 8<<20 {
		logger.Warnf("Pipe max size has to be at least 8 MiB to enable splice: %d", maxPipeSize)
		return false
	}

	r, w, err := os.Pipe()
	if err != nil {
		logger.Errorf("Cannot create pipe: %s", err)
		return false
	}
	size, errno := fcntl(r.Fd(), F_GETPIPE_SZ, 0)
	if errno == 0 {
		_, errno = fcntl(r.Fd(), F_SETPIPE_SZ, 2*size)
	}
	r.Close()
	w.Close()
	if errno != 0 {
		logger.Warnf("Get/Set pipe size failed: %s", errno)
		return false
	}

	// fd, err := syscall.Open("/dev/null", os.O_WRONLY, 0)
	// if err != nil {
	// 	logger.Errorf("Open null device: %s", err)
	// 	return false
	// }
	// NullFD = uintptr(fd)
	// PipePools = [2]*pipePool{{pipeSize: 256 << 10}, {pipeSize: 5 << 20}}
	return true
}

/* --- Pipe --- */
type Pipe struct {
	r, w int
	size int
	cap  int
	// pool *pipePool
}

func NewPipe(cap int) (*Pipe, error) {
	if cap > maxPipeSize {
		return nil, fmt.Errorf("too big cap %d > max pipe size %d", cap, maxPipeSize)
	}
	fds := make([]int, 2)
	if err := syscall.Pipe2(fds, syscall.O_NONBLOCK); err != nil {
		return nil, err
	}
	newcap, errno := fcntl(uintptr(fds[0]), F_SETPIPE_SZ, cap)
	if errno != 0 || newcap < cap {
		return nil, fmt.Errorf("set pipe size to %d: %d %s", cap, newcap, errno)
	}
	return &Pipe{r: fds[0], w: fds[1], cap: cap}, nil
}

func SubPipe(p *Pipe, size int) *Pipe {
	return &Pipe{r: p.r, w: p.w, cap: p.cap, size: size}
}

func (p *Pipe) Size() int {
	if p == nil {
		return 0
	}
	return p.size
}

func (p *Pipe) Close() error {
	err1 := syscall.Close(p.r)
	err2 := syscall.Close(p.w)
	if err1 != nil {
		return err1
	}
	return err2
}

func (p *Pipe) Read(d []byte) (n int, err error) {
	return syscall.Read(p.r, d)
}

func (p *Pipe) Write(d []byte) (n int, err error) {
	n, err = syscall.Write(p.w, d)
	if err == nil {
		p.size += n
	}
	return
}

func (p *Pipe) ReadFd() uintptr {
	return uintptr(p.r)
}

func (p *Pipe) WriteFd() uintptr {
	return uintptr(p.w)
}

// func (p *Pipe) LoadFromAt(fd uintptr, sz int, off int64) (int, error) {
// 	n, err := syscall.Splice(int(fd), &off, p.w, nil, sz, 0)
// 	return int(n), err
// }

// func (p *Pipe) LoadFrom(fd uintptr, sz int) (int, error) {
// 	if sz > p.cap {
// 		return 0, fmt.Errorf("LoadFrom: not enough space %d, %d",
// 			sz, p.cap)
// 	}

// 	n, err := syscall.Splice(int(fd), nil, p.w, nil, sz, 0)
// 	if err != nil {
// 		err = os.NewSyscallError("Splice load from", err)
// 	}
// 	return int(n), err
// }

// func (p *Pipe) WriteTo(fd uintptr, n int) (int, error) {
// 	m, err := syscall.Splice(p.r, nil, int(fd), nil, int(n), 0)
// 	if err != nil {
// 		err = os.NewSyscallError("Splice write", err)
// 	}
// 	return int(m), err
// }

const _SPLICE_F_NONBLOCK = 0x2

// func (p *Pipe) discard() {
// 	for p.size > 0 {
// 		n, err := syscall.Splice(p.r, nil, int(NullFD), nil, int(p.size), _SPLICE_F_NONBLOCK)
// 		p.size -= int(n)
// 		if err == syscall.EAGAIN {
// 			// all good.
// 			p.size = 0
// 		} else if err != nil {
// 			errR := syscall.Close(p.r)
// 			errW := syscall.Close(p.w)

// 			// This can happen if something closed our fd
// 			// inadvertently (eg. double close)
// 			logger.Panicf("splicing into /dev/null: %v (close R %d '%v', close W %d '%v')", err, p.r, errR, p.w, errW)
// 		}
// 	}
// }

// func (p *Pipe) Release() {
// 	if p.pool == nil {
// 		logger.Errorf("Orphan pipe?")
// 		return
// 	}
// 	p.pool.done(p)
// }

/* --- Pool --- */
/*
var PipePools [2]*pipePool // 0: pool with 256K pipes for reqs, 1: pool with 5M pipes for blocks

type pipePool struct {
	sync.Mutex
	unused    []*Pipe
	usedCount int
	pipeSize  int
}

func GetPipe(size int) (p *Pipe, err error) {
	if size <= 128<<10 {
		return PipePools[0].get()
	} else if size <= 4<<20 {
		return PipePools[1].get()
	} else {
		return nil, fmt.Errorf("invalid pipe size %d", size)
	}
}

func (pp *pipePool) get() (*Pipe, error) {
	pp.Lock()
	defer pp.Unlock()

	pp.usedCount++
	if l := len(pp.unused); l > 0 {
		p := pp.unused[l-1]
		pp.unused = pp.unused[:l-1]
		if p.size != 0 {
			logger.Errorf("Invalid new pipe size %p: %+v", p, p)
			if err := p.Close(); err != nil {
				logger.Warnf("Close pipe %p %+v: %s", p, p, err)
			}
		} else {
			return p, nil
		}
	}
	p, err := newPipe(pp.pipeSize)
	if p != nil {
		p.pool = pp
	}
	return p, err
}

 func (pp *pipePool) used() (n int) {
	 pp.Lock()
	 n = pp.usedCount
	 pp.Unlock()
	 return n
 }

 func (pp *pipePool) total() int {
	 pp.Lock()
	 n := pp.usedCount + len(pp.unused)
	 pp.Unlock()
	 return n
 }
*/

// func (pp *pipePool) done(p *Pipe) {
// 	p.discard()
// 	pp.Lock()
// 	pp.usedCount--
// 	pp.unused = append(pp.unused, p)
// 	pp.Unlock()
// }

/*
 func (pp *pipePool) drop(p *Pipe) {
	 p.Close()
	 pp.Lock()
	 pp.usedCount--
	 pp.Unlock()
 }

 func (pp *pipePool) clear() {
	 pp.Lock()
	 for _, p := range pp.unused {
		 p.Close()
	 }
	 pp.unused = pp.unused[:0]
	 pp.Unlock()
 }
*/

// TODO: use vmsplice with SPLICE_F_GIFT?
// func PipeFromBuffer(buf []byte) (*Pipe, error) {
// 	p, err := GetPipe(len(buf))
// 	if err != nil {
// 		return nil, err
// 	}
// 	n, err := p.Write(buf)
// 	p.size += n
// 	if err != nil {
// 		p.Release()
// 		return nil, err
// 	}
// 	if p.size != len(buf) {
// 		logger.Errorf("Got pipe size %d != expect size %d", p.size, len(buf))
// 	}
// 	return p, nil
// }

// func PipeFromFd(fd int, size int, off int64) (*Pipe, error) {
// 	p, err := GetPipe(size)
// 	if err != nil {
// 		return nil, err
// 	}
// 	n, err := p.LoadFromAt(uintptr(fd), size, off)
// 	_ = syscall.Close(fd)
// 	p.size += n
// 	if err != nil {
// 		p.Release()
// 		return nil, err
// 	}
// 	if p.size != size {
// 		logger.Errorf("Got pipe size %d != expect size %d", p.size, size)
// 	}
// 	return p, nil
// }

// func PipeFromPipes(ps []*Pipe, size int) (*Pipe, error) {
// 	switch len(ps) {
// 	case 0:
// 		return nil, nil
// 	case 1:
// 		return ps[0], nil
// 	default:
// 		pipe, err := GetPipe(size)
// 		if err != nil {
// 			return nil, err
// 		}
// 		var n int
// 		for _, p := range ps {
// 			n, err = pipe.LoadFrom(uintptr(p.r), p.size)
// 			p.size -= n
// 			pipe.size += n
// 			if err != nil {
// 				break
// 			}
// 		}
// 		for _, p := range ps {
// 			p.Release()
// 		}
// 		if err != nil {
// 			pipe.Release()
// 			return nil, err
// 		}
// 		if pipe.size != size {
// 			logger.Warnf("Got pipe size %d != expect size %d", pipe.size, size)
// 		}
// 		return pipe, nil
// 	}
// }

// func PipeFromPipe(p *Pipe, size int) (*Pipe, error) {
// 	if p.size < size {
// 		return nil, fmt.Errorf("not enough data %d < %d", p.size, size)
// 	}
// 	// TODO: add refcount, so we can use the same pipe if the size matches

// 	pipe, err := GetPipe(size)
// 	if err != nil {
// 		return nil, err
// 	}
// 	n, err := pipe.LoadFrom(uintptr(p.r), size)
// 	p.size -= n
// 	pipe.size += n
// 	if err != nil {
// 		pipe.Release()
// 		return nil, err
// 	}
// 	if pipe.size != size {
// 		logger.Warnf("Got pipe size %d != expect size %d", pipe.size, size)
// 	}
// 	return pipe, nil
// }
