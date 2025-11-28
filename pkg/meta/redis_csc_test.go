//go:build !noredis

package meta

import (
	"context"
	"testing"
	"time"

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
		name1, name2 := cache.entryName(ino, "f1"), cache.entryName(ino, "f2")
		cache.entryCache.Add(name1, &cachedEntry{})
		cache.entryCache.Add(name2, &cachedEntry{})

		err := m.rdb.HSet(ctx, m.entryKey(ino), "f1", "c1", "f2", "c2").Err()
		require.NoError(t, err)

		_, ok := cache.entryCache.Get(name1)
		require.False(t, ok)
		_, ok = cache.entryCache.Get(name2)
		require.False(t, ok)

		cache.entryCache.Add(name1, &cachedEntry{})
		cache.entryCache.Add(name2, &cachedEntry{})
		err = m.rdb.HDel(ctx, m.entryKey(ino), "f1", "f2").Err()
		require.NoError(t, err)

		_, ok = cache.entryCache.Get(name1)
		require.False(t, ok)
		_, ok = cache.entryCache.Get(name2)
		require.False(t, ok)
	})
}
