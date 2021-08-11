package chunk

import (
	"errors"
	"fmt"
	"github.com/juicedata/juicefs/pkg/utils"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type tieredDiskItem struct {
	size  int32
	atime uint32
}

type tieredDiskStore struct {
	tieredManager *tieredManager
	totalPages    int64
	sync.Mutex
	dir       string
	mode      os.FileMode
	capacity  int64
	freeRatio float32
	pending   chan pendingFile
	pages     map[string]*Page

	used    int64
	keys    map[string]tieredDiskItem
	scanned bool
}

func (c *tieredDiskStore) createDir(dir string) {
	// who can read the cache, should be able to access the directories and add new file.
	readmode := c.mode & 0444
	mode := c.mode | (readmode >> 2) | (readmode >> 1)
	if st, err := os.Stat(dir); os.IsNotExist(err) {
		if filepath.Dir(dir) != dir {
			c.createDir(filepath.Dir(dir))
		}
		_ = os.Mkdir(dir, mode)
		// umask may remove some permisssions
		_ = os.Chmod(dir, mode)
	} else if strings.HasPrefix(dir, c.dir) && err == nil && st.Mode() != mode {
		changeMode(dir, st, mode)
	}
}

func (c *tieredDiskStore) curFreeRatio() (float32, float32) {
	total, free, files, ffree := getDiskUsage(c.dir)
	return float32(free) / float32(total), float32(ffree) / float32(files)
}

func (c *tieredDiskStore) usedMemory() int64 {
	return atomic.LoadInt64(&c.totalPages)
}

func (c *tieredDiskStore) stats() (int64, int64) {
	c.Lock()
	defer c.Unlock()
	return int64(len(c.pages) + len(c.keys)), c.used + c.usedMemory()
}

func (c *tieredDiskStore) savePath(key string) string {
	return filepath.Join(c.dir, key)
}

func (c *tieredDiskStore) add(key string, size int32, atime uint32) {
	c.Lock()
	defer c.Unlock()
	it, ok := c.keys[key]
	if ok {
		c.used -= int64(it.size + 4096)
	}
	if atime == 0 {
		// update size of staging block
		c.keys[key] = tieredDiskItem{size, it.atime}
	} else {
		c.keys[key] = tieredDiskItem{size, atime}
	}
	c.used += int64(size + 4096)

	if c.used > c.capacity {
		logger.Debugf("Out of local disk when add new data (%s): %d blocks (%d MB)", c.dir, len(c.keys), c.used>>20)
	}
}

func (c *tieredDiskStore) uploaded(key string, size int) {
	c.add(key, int32(size), 0)
}

func (c *tieredDiskStore) flushPage(path string, data []byte, sync bool) error {
	c.createDir(filepath.Dir(path))
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE, c.mode)
	if err != nil {
		logger.Infof("Can't create tiered disk file %s: %s", tmp, err)
		return err
	}
	_, err = f.Write(data)
	if err != nil {
		logger.Warnf("Write to tiered disk file %s failed: %s", tmp, err)
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if sync {
		err = f.Sync()
		if err != nil {
			logger.Warnf("sync tiered disk file %s failed: %s", tmp, err)
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	err = f.Close()
	if err != nil {
		logger.Warnf("Close tiered disk %s failed: %s", tmp, err)
		_ = os.Remove(tmp)
		return err
	}
	err = os.Rename(tmp, path)
	if err != nil {
		logger.Infof("Rename tiered disk %s -> %s failed: %s", tmp, path, err)
		_ = os.Remove(tmp)
	}
	return err
}

func (c *tieredDiskStore) save(key string, data []byte) (string, error) {
	if c.used > c.capacity {
		return "", errors.New(fmt.Sprintf("out of tiered disk. capacity:%d used:%d", c.capacity, c.used))
	}
	tieredDiskPath := c.savePath(key)
	err := c.flushPage(tieredDiskPath, data, false)
	if err == nil && c.capacity > 0 {
		path := c.tieredManager.tieredStore.cachedStore.bcache.(*cacheManager).getStore(key).cachePath(key)
		c.createDir(filepath.Dir(path))
		if err := os.Link(tieredDiskPath, path); err == nil {
			c.add(key, int32(len(data)), uint32(time.Now().Unix()))
		} else {
			logger.Warnf("link %s to %s failed: %s", tieredDiskPath, path, err)
		}
	}
	return tieredDiskPath, err
}

func (c *tieredDiskStore) load(key string) (ReadCloser, error) {
	return c.tieredManager.tieredStore.cachedStore.bcache.(*cacheManager).getStore(key).load(key)
}

func (c *tieredDiskStore) remove(key string) {
	c.Lock()
	path := c.savePath(key)
	if c.keys[key].atime > 0 {
		c.used -= int64(c.keys[key].size + 4096)
		delete(c.keys, key)
	} else if c.scanned {
		path = "" // not existed
	}
	c.Unlock()
	if path != "" {
		_ = os.Remove(path)
		_ = os.Remove(c.savePath(key))
	}
}

func newTieredDiskStore(tieredManager *tieredManager, dir string, tieredDiskSize int64, pendingPages int, config *TieredConfig) *tieredDiskStore {
	if config.TieredDiskMode == 0 {
		config.TieredDiskMode = 0600 // only owner can read/write cache
	}
	if config.TieredDiskFreeSpace == 0.0 {
		config.TieredDiskFreeSpace = 0.1 // 10%
	}
	c := &tieredDiskStore{
		tieredManager: tieredManager,
		dir:           dir,
		mode:          config.TieredDiskMode,
		capacity:      tieredDiskSize,
		freeRatio:     config.TieredDiskFreeSpace,
		keys:          make(map[string]tieredDiskItem),
		pending:       make(chan pendingFile, pendingPages),
		pages:         make(map[string]*Page),
	}
	c.createDir(c.dir)
	br, fr := c.curFreeRatio()
	if br < c.freeRatio || fr < c.freeRatio {
		logger.Warnf("No enough space (%d%%) or inodes (%d%%) for tired disk in %s: free ratio should be >= %d%%", int(br*100), int(fr*100), c.dir, int(c.freeRatio*100))
	}
	logger.Infof("Disk (%s): capacity (%d MB), free ratio (%d%%), max pending pages (%d)", c.dir, c.capacity>>20, int(c.freeRatio*100), pendingPages)
	return c
}

type tieredManager struct {
	stores      []*tieredDiskStore
	tieredStore *tieredStore
}

type TieredManager interface {
	save(key string, data []byte) (string, error)
	remove(key string)
	load(key string) (ReadCloser, error)
	uploaded(key string, size int)
	stats() (int64, int64)
	usedMemory() int64
}

func newTieredManager(tieredStore *tieredStore, config *TieredConfig) TieredManager {

	var dirs []string
	for _, d := range utils.SplitDir(config.TieredDiskDir) {
		dd := expandDir(d)
		dirs = append(dirs, dd...)
	}
	if len(dirs) == 0 {
		logger.Warnf("No tired disk existed")
	}
	sort.Strings(dirs)
	dirSize := config.TieredDiskSize << 20
	dirSize /= int64(len(dirs))
	m := &tieredManager{
		stores:      make([]*tieredDiskStore, len(dirs)),
		tieredStore: tieredStore,
	}
	// 20% of buffer could be used for pending pages
	pendingPages := config.TieredDiskBufferSize * 2 / 10 / config.BlockSize / len(dirs)
	for i, d := range dirs {
		m.stores[i] = newTieredDiskStore(m, strings.TrimSpace(d)+string(filepath.Separator), dirSize, pendingPages, config)
	}
	return m
}

func (m *tieredManager) getStore(key string) *tieredDiskStore {
	return m.stores[keyHash(key)%uint32(len(m.stores))]
}

func (m *tieredManager) usedMemory() int64 {
	var used int64
	for _, s := range m.stores {
		used += s.usedMemory()
	}
	return used
}

func (m *tieredManager) stats() (int64, int64) {
	var cnt, used int64
	for _, s := range m.stores {
		c, u := s.stats()
		cnt += c
		used += u
	}
	return cnt, used
}

func (m *tieredManager) save(key string, data []byte, ) (string, error) {
	if len(m.stores) == 0 {
		return "", errors.New("no tiered local disk dir")
	}
	return m.getStore(key).save(key, data)
}

func (m *tieredManager) load(key string) (ReadCloser, error) {
	if len(m.stores) == 0 {
		return nil, errors.New("no tiered local disk dir")
	}
	return m.getStore(key).load(key)
}

func (m *tieredManager) remove(key string) {
	if len(m.stores) > 0 {
		m.getStore(key).remove(key)
	}
}

func (m *tieredManager) uploaded(key string, size int) {
	if len(m.stores) > 0 {
		m.getStore(key).uploaded(key, size)
	}
}
