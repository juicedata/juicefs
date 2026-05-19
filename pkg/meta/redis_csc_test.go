//go:build !noredis

package meta

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9/push"
	"github.com/stretchr/testify/require"
)

func mockRedisCSCMeta(t *testing.T) *redisMeta {
	m, err := newRedisMeta("redis", "127.0.0.1:6379/10?client-cache=true", testConfig())
	require.NoError(t, err, "failed to create redis meta")
	require.Equal(t, "redis", m.Name(), "meta name should be redis")
	return m.(*redisMeta)
}

func TestRedisCache(t *testing.T) {
	ctx := context.Background()
	m := mockRedisCSCMeta(t)
	_ = m.rdb.FlushAll(ctx)
	defer m.Shutdown()
	defer m.cache.close()

	var err error
	t.Run("invalidation handling", func(t *testing.T) {
		cache := m.cache
		ino := Ino(100)
		attr := &Attr{Typ: TypeFile, Mode: 0644}
		cache.inodeCache.Add(ino, attr.Marshal())
		if _, ok := cache.inodeCache.Get(ino); !ok {
			t.Fatal("inode should be in cache")
		}

		err = m.rdb.Set(ctx, m.inodeKey(ino), m.marshal(&Attr{Mode: 0755}), 0).Err()
		require.NoError(t, err, "failed to set key %d", ino)
		dumIno := Ino(101)
		err = m.rdb.Set(ctx, m.inodeKey(dumIno), m.marshal(&Attr{Mode: 0755}), 0).Err()
		require.NoError(t, err, "failed to set key %d", dumIno)
		time.Sleep(3 * time.Second)
		if _, ok := cache.inodeCache.Get(Ino(100)); ok {
			t.Fatal("inode should be invalidated and removed from cache")
		}

		cache.entryCache.Add(cache.entryName(101, "file"), &cachedEntry{})
		m.rdb.HSet(ctx, m.entryKey(100), "file", "content").Err()
	})
	t.Run("cache expiration", func(t *testing.T) {
		shortExpiry := 50 * time.Millisecond
		cache := newRedisCache("jfs", 1000, shortExpiry, 0)
		attr := &Attr{Typ: TypeFile, Mode: 0644}
		cache.inodeCache.Add(Ino(102), attr.Marshal())
		time.Sleep(3 * shortExpiry)
		if _, ok := cache.inodeCache.Get(Ino(102)); ok {
			t.Fatal("inode should be expired")
		}
	})

	t.Run("inode hook", func(t *testing.T) {
		cache := m.cache
		ino := Ino(103)
		attr := &Attr{Typ: TypeFile, Length: 10}
		cache.inodeCache.Add(ino, attr.Marshal())

		data, err := m.rdb.Get(ctx, m.inodeKey(ino)).Bytes()
		require.NoError(t, err, "failed to get inode")
		attr2 := &Attr{}
		attr2.Unmarshal(data)
		attr2.Full = false
		require.Equal(t, *attr, *attr2)

		attr3 := &Attr{Typ: TypeFile, Length: 20}
		err = m.rdb.Set(ctx, m.inodeKey(ino), attr3.Marshal(), 0).Err()
		require.NoError(t, err)
		_, ok := cache.inodeCache.Get(ino)
		require.False(t, ok)
	})

	t.Run("entry hook", func(t *testing.T) {
		cache := m.cache
		ino := Ino(104)
		gen := cache.entryTerm(ino)
		name1, name2 := cache.entryName(ino, "f1"), cache.entryName(ino, "f2")
		cache.entryCache.Add(name1, &cachedEntry{term: gen})
		cache.entryCache.Add(name2, &cachedEntry{term: gen})

		err := m.rdb.HSet(ctx, m.entryKey(ino), "f1", "c1", "f2", "c2").Err()
		require.NoError(t, err)

		_, ok := cache.entryCache.Get(name1)
		require.False(t, ok)
		_, ok = cache.entryCache.Get(name2)
		require.False(t, ok)

		cache.entryCache.Add(name1, &cachedEntry{term: gen})
		cache.entryCache.Add(name2, &cachedEntry{term: gen})
		err = m.rdb.HDel(ctx, m.entryKey(ino), "f1", "f2").Err()
		require.NoError(t, err)

		_, ok = cache.entryCache.Get(name1)
		require.False(t, ok)
		_, ok = cache.entryCache.Get(name2)
		require.False(t, ok)
	})

	t.Run("entry generation invalidation", func(t *testing.T) {
		cache := m.cache
		parent := Ino(105)
		name := cache.entryName(parent, "f")
		gen := cache.entryTerm(parent)
		cache.entryCache.Add(name, &cachedEntry{ino: 106, term: gen, Attr: Attr{Typ: TypeFile}})

		err := cache.HandlePushNotification(ctx, push.NotificationHandlerContext{}, []interface{}{"invalidate", []interface{}{m.entryKey(parent)}})
		require.NoError(t, err)
		require.Equal(t, gen+1, cache.entryTerm(parent))

		entry, ok := cache.entryCache.Get(name)
		require.True(t, ok)
		require.NotEqual(t, cache.entryTerm(parent), entry.term)
	})

	t.Run("entry term expires after idle", func(t *testing.T) {
		cache := newRedisCache("jfs", 1000, 10*time.Millisecond, 0)
		parent := Ino(106)

		require.Equal(t, uint64(1), cache.bumpEntryTerm(parent))
		time.Sleep(11 * cache.expiry)
		require.Equal(t, uint64(0), cache.entryTerm(parent))
	})

	t.Run("entry term refreshes on access", func(t *testing.T) {
		cache := newRedisCache("jfs", 1000, 10*time.Millisecond, 0)
		parent := Ino(107)

		require.Equal(t, uint64(1), cache.bumpEntryTerm(parent))
		time.Sleep(6 * cache.expiry)
		require.Equal(t, uint64(1), cache.entryTerm(parent))
		time.Sleep(6 * cache.expiry)
		require.Equal(t, uint64(1), cache.entryTerm(parent))
	})

	t.Run("entry mark refill requires current generation", func(t *testing.T) {
		cache := m.cache
		parent := Ino(107)
		name := cache.entryName(parent, "f")
		gen := cache.entryTerm(parent)
		cache.entryCache.Add(name, &cachedEntry{term: gen})

		cache.bumpEntryTerm(parent)
		added, _ := cache.entryCache.AddIf(name, &cachedEntry{ino: 108, term: gen}, func(oldEntry *cachedEntry, exists bool) bool {
			return exists && oldEntry.isMark() && oldEntry.term == gen && cache.entryTerm(parent) == gen
		})
		require.False(t, added)

		entry, ok := cache.entryCache.Get(name)
		require.True(t, ok)
		require.True(t, entry.isMark())
	})

	t.Run("entry mark refill requires existing mark", func(t *testing.T) {
		cache := m.cache
		parent := Ino(109)
		name := cache.entryName(parent, "f")
		gen := cache.entryTerm(parent)
		cache.entryCache.Add(name, &cachedEntry{term: gen})
		cache.entryCache.Remove(name)

		added, _ := cache.entryCache.AddIf(name, &cachedEntry{ino: 110, term: gen}, func(oldEntry *cachedEntry, exists bool) bool {
			return exists && oldEntry.isMark() && oldEntry.term == gen && cache.entryTerm(parent) == gen
		})
		require.False(t, added)
		_, ok := cache.entryCache.Get(name)
		require.False(t, ok)
	})

	t.Run("stale entry is not used while entry term is active", func(t *testing.T) {
		cache := newRedisCache("jfs", 1000, 20*time.Millisecond, 0)
		parent := Ino(110)
		name := cache.entryName(parent, "f")
		cache.entryCache.Add(name, &cachedEntry{ino: 111, term: cache.entryTerm(parent), Attr: Attr{Typ: TypeFile}})

		cache.bumpEntryTerm(parent)
		entry, ok := cache.entryCache.Get(name)
		require.True(t, ok)
		require.False(t, entry.term >= cache.entryTerm(parent) && !entry.isMark())
	})

	t.Run("old entry expires before idle entry term", func(t *testing.T) {
		cache := newRedisCache("jfs", 1000, 10*time.Millisecond, 0)
		parent := Ino(112)
		name := cache.entryName(parent, "f")
		cache.entryCache.Add(name, &cachedEntry{ino: 113, term: cache.entryTerm(parent), Attr: Attr{Typ: TypeFile}})
		cache.bumpEntryTerm(parent)

		time.Sleep(2 * cache.expiry)
		_, ok := cache.entryCache.Get(name)
		require.False(t, ok)
		require.Equal(t, uint64(1), cache.entryTerm(parent))
	})

	t.Run("active entry term outlives repeated stale entry checks", func(t *testing.T) {
		cache := newRedisCache("jfs", 1000, 10*time.Millisecond, 0)
		parent := Ino(114)
		name := cache.entryName(parent, "f")
		cache.entryCache.Add(name, &cachedEntry{ino: 115, term: cache.entryTerm(parent), Attr: Attr{Typ: TypeFile}})
		cache.bumpEntryTerm(parent)

		for range 3 {
			time.Sleep(4 * cache.expiry)
			entry, ok := cache.entryCache.Get(name)
			if ok {
				require.False(t, entry.term >= cache.entryTerm(parent) && !entry.isMark())
			} else {
				require.Equal(t, uint64(1), cache.entryTerm(parent))
			}
		}
		require.Equal(t, uint64(1), cache.entryTerm(parent))
	})

	t.Run("concurrent stale refills cannot overwrite newer mark", func(t *testing.T) {
		cache := newRedisCache("jfs", 1000, time.Second, 0)
		parent := Ino(116)
		name := cache.entryName(parent, "f")
		oldTerm := cache.entryTerm(parent)
		cache.entryCache.Add(name, &cachedEntry{term: oldTerm})
		newTerm := cache.bumpEntryTerm(parent)
		cache.entryCache.Add(name, &cachedEntry{term: newTerm})

		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(ino Ino) {
				defer wg.Done()
				cache.entryCache.AddIf(name, &cachedEntry{ino: ino, term: oldTerm}, func(oldEntry *cachedEntry, exists bool) bool {
					return exists && oldEntry.isMark() && oldEntry.term == oldTerm && cache.entryTerm(parent) == oldTerm
				})
			}(Ino(117 + i))
		}
		wg.Wait()

		entry, ok := cache.entryCache.Get(name)
		require.True(t, ok)
		require.True(t, entry.isMark())
		require.Equal(t, newTerm, entry.term)
	})
}
