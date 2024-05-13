package chunk

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

var (
	numIOErrToUnstable         uint32  = 3                // from normal to unstable
	minIOSuccToNormal          uint32  = 3 * 60           // from unstable to normal
	maxIOErrPercentageToNormal float64 = 0                // from unstable to normal
	maxDurToDown                       = 30 * time.Minute // from unstable to down
	maxConcurrencyForUnstable  int64   = 10
	tickDurForNormal                   = 1 * time.Minute
	tickDurForUnstable                 = 1 * time.Minute

	probeDur = 100 * time.Millisecond
	probeDir = "probe"
)

var (
	errCacheDown       = errors.New("cache down")
	errUnstableCoLimit = fmt.Errorf("exceed concurrency %d limit for unstable disk cache", maxConcurrencyForUnstable)
)

var diskStateNames = map[int]string{
	dcUnknown:  "unknown",
	dcNormal:   "normal",
	dcUnstable: "unstable",
	dcDown:     "down",
}

const (
	dcUnknown = iota
	dcNormal
	dcUnstable
	dcDown
)

const (
	eventUnknown = iota
	eventToNormal
	eventToUnstable
	eventToDown
)

// dcState disk cache state
type dcState interface {
	init(cs *cacheStore)
	tick()
	stop()
	state() int
	checkCacheOp() error
	beforeCacheOp()
	afterCacheOp()
	onIOErr()
	onIOSucc()
}

type baseDC struct {
	cache  *cacheStore
	stopCh chan struct{}
}

func newDCState(state int, cs *cacheStore) dcState {
	var s dcState
	switch state {
	case dcNormal:
		s = &normalDC{}
	case dcUnstable:
		s = &unstableDC{}
	case dcDown:
		s = &downDC{}
	}
	s.init(cs)
	s.tick()
	return s
}

func (dc *baseDC) init(cs *cacheStore) {
	dc.cache = cs
	dc.stopCh = make(chan struct{})
}

func (dc *baseDC) stop() {
	close(dc.stopCh)
}
func (dc *baseDC) onIOErr()            {}
func (dc *baseDC) onIOSucc()           {}
func (dc *baseDC) state() int          { return dcUnknown }
func (dc *baseDC) tick()               {}
func (dc *baseDC) checkCacheOp() error { return nil }
func (dc *baseDC) beforeCacheOp()      {}
func (dc *baseDC) afterCacheOp()       {}

type normalDC struct {
	baseDC
	ioErrCnt uint32
}

func (dc *normalDC) state() int { return dcNormal }

func (dc *normalDC) init(cs *cacheStore) {
	dc.baseDC.init(cs)
	_ = os.RemoveAll(dc.cache.cachePath(probeDir))
}

func (dc *normalDC) tick() {
	go func() {
		for {
			select {
			case <-dc.stopCh:
				return
			case <-time.After(tickDurForNormal):
				atomic.StoreUint32(&dc.ioErrCnt, 0)
			}
		}
	}()
}

func (dc *normalDC) onIOErr() {
	cnt := atomic.AddUint32(&dc.ioErrCnt, 1)
	if cnt >= uint32(numIOErrToUnstable) {
		dc.cache.event(eventToUnstable)
	}
}

type unstableDC struct {
	baseDC
	startTime time.Time
	ioErrCnt  uint32
	ioCnt     uint32

	concurrency atomic.Int64
}

func (dc *unstableDC) state() int { return dcUnstable }

func (dc *unstableDC) init(cs *cacheStore) {
	dc.baseDC.init(cs)
	dc.startTime = time.Now()
}

func (dc *unstableDC) onIOErr() {
	atomic.AddUint32(&dc.ioCnt, 1)
	atomic.AddUint32(&dc.ioErrCnt, 1)
}

func (dc *unstableDC) onIOSucc() {
	atomic.AddUint32(&dc.ioCnt, 1)
}

func probeCacheKey(id, size int) string {
	return fmt.Sprintf("%s/%02X/%v/%v_%v_%v", probeDir, id%256, id/1000/1000, id, 0, size)
}

func (dc *unstableDC) tick() {
	go dc.probe()
	go func() {
		ticker := time.NewTicker(tickDurForUnstable)
		defer ticker.Stop()

		for {
			select {
			case <-dc.stopCh:
				return
			case <-ticker.C:
				errCnt, ioCnt := atomic.LoadUint32(&dc.ioErrCnt), atomic.LoadUint32(&dc.ioCnt)
				if ioCnt >= minIOSuccToNormal && float64(errCnt)/float64(ioCnt) <= maxIOErrPercentageToNormal {
					dc.cache.event(eventToNormal)
				} else if time.Since(dc.startTime) >= maxDurToDown {
					dc.cache.event(eventToDown)
				} else {
					atomic.StoreUint32(&dc.ioErrCnt, 0)
					atomic.StoreUint32(&dc.ioCnt, 0)
				}
			}
		}
	}()
}

func (dc *unstableDC) probe() {
	data := []byte{1, 2, 3}
	page := NewPage(data)
	defer page.Release()
	cnt := 0

	for {
		select {
		case <-dc.stopCh:
			return
		default:
			cnt++
			start := time.Now()
			dc.doProbe(probeCacheKey(cnt, len(data)), page)
			diff := probeDur - time.Since(start)
			if diff > 0 {
				time.Sleep(diff)
			}
		}
	}
}

func (dc *unstableDC) doProbe(key string, page *Page) {
	dc.cache.cache(key, page, true)
	if _, err := dc.cache.load(key); err != nil {
		dc.cache.remove(key)
	}
}

func (dc *unstableDC) beforeCacheOp() { dc.concurrency.Add(1) }
func (dc *unstableDC) afterCacheOp()  { dc.concurrency.Add(-1) }

func (dc *unstableDC) checkCacheOp() error {
	if dc.concurrency.Load() >= maxConcurrencyForUnstable {
		return errUnstableCoLimit
	}
	return nil
}

type downDC struct {
	baseDC
}

func (dc *downDC) state() int          { return dcDown }
func (dc *downDC) checkCacheOp() error { return errCacheDown }

func (cache *cacheStore) event(eventType int) {
	cache.stateLock.Lock()
	defer cache.stateLock.Unlock()
	state := cache.state.state()
	switch state {
	case dcNormal:
		if eventType == eventToUnstable {
			cache.state.stop()
			cache.state = newDCState(dcUnstable, cache)
		}
	case dcUnstable:
		switch eventType {
		case eventToNormal:
			cache.state.stop()
			cache.state = newDCState(dcNormal, cache)
		case eventToDown:
			cache.state.stop()
			cache.state = newDCState(dcDown, cache)
		}
	}
	logger.Warnf("disk cache state change from %s to %s", diskStateNames[state], diskStateNames[cache.state.state()])
}

func getEnvs() {
	if os.Getenv("JFS_MAX_IO_DURATION") != "" {
		dur, _ := strconv.Atoi(os.Getenv("JFS_MAX_IO_DURATION"))
		maxIODur = time.Duration(dur) * time.Second
		logger.Infof("set maxIODur to %d", maxIODur)
	}
	if os.Getenv("JFS_MAX_IO_ERR_PERCENTAGE") != "" {
		percentage, _ := strconv.ParseFloat(os.Getenv("JFS_MAX_IO_ERR_PERCENTAGE"), 64)
		maxIOErrPercentageToNormal = 1 - percentage
		logger.Infof("set maxIOErrPercentageToNormal to %f", maxIOErrPercentageToNormal)
	}
	if os.Getenv("JFS_MAX_DURATION_TO_DOWN") != "" {
		dur, _ := strconv.Atoi(os.Getenv("JFS_MAX_DURATION_TO_DOWN"))
		maxDurToDown = time.Duration(dur) * time.Minute
		logger.Infof("set maxDurToDown to %d minutes", maxDurToDown)
	}
	if os.Getenv("JFS_MAX_CONCURRENCY_FOR_UNSTABLE") != "" {
		maxConcurrencyForUnstable, _ = strconv.ParseInt(os.Getenv("JFS_MAX_CONCURRENCY_FOR_UNSTABLE"), 10, 64)
		logger.Infof("set maxConcurrencyForUnstable to %d", maxConcurrencyForUnstable)
	}
}
