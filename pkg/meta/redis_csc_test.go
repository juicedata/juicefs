//go:build !noredis

package meta

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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
	t.Run("invalidations resume after the tracking connection reconnects", func(t *testing.T) {
		other := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 10})
		defer other.Close()
		ino := Ino(280)
		require.NoError(t, other.Set(ctx, m.inodeKey(ino), m.marshal(&Attr{Typ: TypeFile}), 0).Err())
		require.NoError(t, other.Do(ctx, "CLIENT", "KILL", "TYPE", "pubsub").Err())
		require.Eventually(t, func() bool {
			// cache the attr, then change it from another client
			m.cache.inodeCache.Remove(ino)
			if err := m.rdb.Get(ctx, m.inodeKey(ino)).Err(); err != nil {
				return false
			}
			if _, ok := m.cache.inodeCache.Get(ino); !ok {
				return false
			}
			if err := other.Set(ctx, m.inodeKey(ino), m.marshal(&Attr{Typ: TypeFile}), 0).Err(); err != nil {
				return false
			}
			time.Sleep(200 * time.Millisecond)
			_, ok := m.cache.inodeCache.Get(ino)
			return !ok
		}, 10*time.Second, 100*time.Millisecond)
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

	t.Run("cached lookup returns current attrs", func(t *testing.T) {
		other := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 10})
		defer other.Close()
		parent, ino := Ino(300), Ino(301)
		require.NoError(t, other.Set(ctx, m.inodeKey(ino), m.marshal(&Attr{Typ: TypeFile, Mode: 0644, Length: 301}), 0).Err())
		require.NoError(t, other.HSet(ctx, m.entryKey(parent), "f", m.packEntry(TypeFile, ino)).Err())
		require.Eventually(t, func() bool { return m.cache.entryTerm(parent) > 0 }, 3*time.Second, 10*time.Millisecond)
		var found Ino
		var attr Attr
		require.Equal(t, syscall.Errno(0), m.doLookup(Background(), parent, "f", &found, &attr))
		require.Equal(t, uint64(301), attr.Length)
		_, ok := m.cache.entryCache.Get(m.cache.entryName(parent, "f"))
		require.True(t, ok)

		// another client changes the file but not the directory
		attr.AccessACL, attr.DefaultACL = 7, 8
		require.NoError(t, other.Set(ctx, m.inodeKey(ino), m.marshal(&Attr{Typ: TypeFile, Mode: 0600, Length: 1}), 0).Err())
		require.Eventually(t, func() bool {
			return m.doLookup(Background(), parent, "f", &found, &attr) == 0 && attr.Length == 1
		}, 3*time.Second, 10*time.Millisecond)
		require.Equal(t, ino, found)
		require.Equal(t, uint16(0600), attr.Mode)
		require.Zero(t, attr.AccessACL)
		require.Zero(t, attr.DefaultACL)
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

	t.Run("reads in transactions bypass the cache", func(t *testing.T) {
		ino := Ino(340)
		fresh, stale := m.marshal(&Attr{Typ: TypeFile, Length: 2}), m.marshal(&Attr{Typ: TypeFile, Length: 1})
		require.NoError(t, m.rdb.Set(ctx, m.inodeKey(ino), fresh, 0).Err())
		time.Sleep(100 * time.Millisecond) // let the invalidation pass
		// an invalidation that has not arrived yet
		m.cache.inodeCache.Add(ino, stale)
		v, err := m.rdb.Get(ctx, m.inodeKey(ino)).Bytes()
		require.NoError(t, err)
		require.Equal(t, stale, v)

		err = m.rdb.Watch(ctx, func(tx *redis.Tx) error {
			v, err := tx.Get(ctx, m.inodeKey(ino)).Bytes()
			require.NoError(t, err)
			require.Equal(t, fresh, v)
			return nil
		}, m.inodeKey(ino))
		require.NoError(t, err)

		// The transaction must not disable caching on the parent client.
		v, err = m.rdb.Get(ctx, m.inodeKey(ino)).Bytes()
		require.NoError(t, err)
		require.Equal(t, stale, v)
	})

	t.Run("entry generation invalidation", func(t *testing.T) {
		cache := m.cache
		parent := Ino(105)
		name := cache.entryName(parent, "f")
		gen := cache.entryTerm(parent)
		cache.entryCache.Add(name, &cachedEntry{ino: 106, term: gen})

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
		cache.entryCache.Add(name, &cachedEntry{ino: 111, term: cache.entryTerm(parent)})

		cache.bumpEntryTerm(parent)
		entry, ok := cache.entryCache.Get(name)
		require.True(t, ok)
		require.False(t, entry.term >= cache.entryTerm(parent) && !entry.isMark())
	})

	t.Run("old entry expires before idle entry term", func(t *testing.T) {
		cache := newRedisCache("jfs", 1000, 10*time.Millisecond, 0)
		parent := Ino(112)
		name := cache.entryName(parent, "f")
		cache.entryCache.Add(name, &cachedEntry{ino: 113, term: cache.entryTerm(parent)})
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
		cache.entryCache.Add(name, &cachedEntry{ino: 115, term: cache.entryTerm(parent)})
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

type redisCacheTestHook struct {
	process func(context.Context, redis.Cmder, redis.ProcessHook) error
}

func (h redisCacheTestHook) DialHook(next redis.DialHook) redis.DialHook { return nil }
func (h redisCacheTestHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return nil
}
func (h redisCacheTestHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { return h.process(ctx, cmd, next) }
}

func TestRedisCachePreload(t *testing.T) {
	for i, name := range []string{"warm attributes", "invalidated during MGET", "unrelated invalidation", "local SET", "tracking reconnect", "directory changed", "missing inode", "read error"} {
		t.Run(name, func(t *testing.T) {
			m := mockRedisCSCMeta(t)
			defer m.Shutdown()
			other := redis.NewClient(m.cache.cli.Options())
			defer other.Close()
			ctx := Background()
			parent, ino := Ino(400+i*3), Ino(401+i*3)
			m.root, m.cache.preload = parent, 10
			want := Attr{Typ: TypeFile, Mode: 0644, Length: 11, Mtime: 2}
			require.NoError(t, other.Set(ctx, m.inodeKey(parent), (&Attr{Typ: TypeDirectory}).Marshal(), 0).Err())
			if name != "missing inode" {
				require.NoError(t, other.Set(ctx, m.inodeKey(ino), want.Marshal(), 0).Err())
			} else {
				require.NoError(t, other.Del(ctx, m.inodeKey(ino)).Err())
			}
			require.NoError(t, other.HSet(ctx, m.entryKey(parent), "f", m.packEntry(TypeFile, ino)).Err())
			require.Eventually(t, func() bool { return m.cache.entryTerm(parent) > 0 }, 3*time.Second, time.Millisecond)
			term := m.cache.entryTerm(parent)
			m.of.Open(ino, &Attr{Typ: TypeFile, Mtime: 1})
			defer m.of.Close(ino)
			m.of.CacheChunk(ino, 0, []Slice{{Id: 1}})

			gets, mgets := 0, 0
			intercept := true
			m.rdb.AddHook(redisCacheTestHook{process: func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
				if cmd.Name() == "get" {
					gets++
				}
				if cmd.Name() != "mget" {
					return next(ctx, cmd)
				}
				mgets++
				if !intercept || cmd.Args()[1] != m.inodeKey(ino) {
					return next(ctx, cmd)
				}
				intercept = false
				if name == "read error" {
					err := errors.New("preload read failed")
					cmd.SetErr(err)
					return err
				}
				err := next(ctx, cmd)
				switch name {
				case "invalidated during MGET":
					want.Length = 12
					require.NoError(t, other.Set(ctx, m.inodeKey(ino), want.Marshal(), 0).Err())
					require.NoError(t, m.cache.HandlePushNotification(ctx, push.NotificationHandlerContext{}, []interface{}{"invalidate", []interface{}{m.inodeKey(ino)}}))
				case "unrelated invalidation":
					require.NoError(t, m.cache.HandlePushNotification(ctx, push.NotificationHandlerContext{}, []interface{}{"invalidate", []interface{}{m.inodeKey(ino + 1)}}))
				case "local SET":
					want.Length = 12
					require.NoError(t, m.rdb.Set(ctx, m.inodeKey(ino), want.Marshal(), 0).Err())
				case "tracking reconnect":
					require.NoError(t, other.Do(ctx, "CLIENT", "KILL", "TYPE", "pubsub").Err())
					require.Eventually(t, func() bool {
						m.cache.preloadMu.Lock()
						defer m.cache.preloadMu.Unlock()
						return m.cache.trackingReady && len(m.cache.preloadPending) == 0
					}, 3*time.Second, time.Millisecond)
				case "directory changed":
					require.NoError(t, other.Set(ctx, m.inodeKey(ino+1), want.Marshal(), 0).Err())
					require.NoError(t, other.HSet(ctx, m.entryKey(parent), "f", m.packEntry(TypeFile, ino+1)).Err())
					require.NoError(t, m.cache.HandlePushNotification(ctx, push.NotificationHandlerContext{}, []interface{}{"invalidate", []interface{}{m.entryKey(parent)}}))
				}
				return err
			}})

			m.preloadCache()
			require.Equal(t, 1, gets, "only the root attribute needs GET")
			require.Equal(t, 1, mgets)
			m.cache.preloadMu.Lock()
			pending := len(m.cache.preloadPending)
			m.cache.preloadMu.Unlock()
			require.Zero(t, pending, "preload must clean up pending markers")
			entry, ok := m.cache.entryCache.Get(m.cache.entryName(parent, "f"))
			if name == "missing inode" || name == "read error" {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, term, entry.term)
			var found Ino
			var attr Attr
			switch name {
			case "warm attributes", "unrelated invalidation":
				data, ok := m.cache.inodeCache.Get(ino)
				require.True(t, ok)
				require.Equal(t, want.Marshal(), data)
				_, cached := m.of.ReadChunk(ino, 0)
				require.False(t, cached, "preload must invalidate old chunk mappings")
				before := gets
				require.Zero(t, m.doLookup(ctx, parent, "f", &found, &attr))
				require.Equal(t, before, gets, "preloaded lookup must not issue GET")
				want.Length = 12
				require.NoError(t, other.Set(ctx, m.inodeKey(ino), want.Marshal(), 0).Err())
				require.Eventually(t, func() bool { _, ok := m.cache.inodeCache.Get(ino); return !ok }, 3*time.Second, time.Millisecond)
			case "invalidated during MGET", "local SET", "tracking reconnect":
				_, ok := m.cache.inodeCache.Get(ino)
				require.False(t, ok, "invalidated response must not refill the cache")
			case "directory changed":
				require.Greater(t, m.cache.entryTerm(parent), entry.term)
				ino++
			}
			require.Zero(t, m.doLookup(ctx, parent, "f", &found, &attr))
			require.Equal(t, ino, found)
			require.Equal(t, want.Length, attr.Length)
		})
	}

	t.Run("multiple batches", func(t *testing.T) {
		m := mockRedisCSCMeta(t)
		defer m.Shutdown()
		other := redis.NewClient(m.cache.cli.Options())
		defer other.Close()
		ctx := Background()
		const count = 4097
		parent := Ino(10000)
		m.root, m.cache.preload = parent, count
		want := (&Attr{Typ: TypeFile, Mode: 0644, Length: 11}).Marshal()
		require.NoError(t, other.Del(ctx, m.entryKey(parent)).Err())
		_, err := other.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, m.inodeKey(parent), (&Attr{Typ: TypeDirectory}).Marshal(), 0)
			for i := 1; i <= count; i++ {
				ino := parent + Ino(i)
				pipe.Set(ctx, m.inodeKey(ino), want, 0)
				pipe.HSet(ctx, m.entryKey(parent), fmt.Sprint(i), m.packEntry(TypeFile, ino))
			}
			return nil
		})
		require.NoError(t, err)
		barrier := parent + count + 1
		require.NoError(t, other.HSet(ctx, m.entryKey(barrier), "ready", "1").Err())
		require.Eventually(t, func() bool { return m.cache.entryTerm(barrier) > 0 }, 3*time.Second, time.Millisecond)
		gets, mgets := 0, 0
		m.rdb.AddHook(redisCacheTestHook{process: func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
			switch cmd.Name() {
			case "get":
				gets++
			case "mget":
				mgets++
				require.LessOrEqual(t, len(cmd.Args())-1, 4096)
			}
			return next(ctx, cmd)
		}})
		m.preloadCache()
		require.Equal(t, 1, gets)
		require.Equal(t, 2, mgets)
		for i := 1; i <= count; i++ {
			var found Ino
			var attr Attr
			require.Zero(t, m.doLookup(ctx, parent, fmt.Sprint(i), &found, &attr))
			require.Equal(t, parent+Ino(i), found)
			require.Equal(t, want, attr.Marshal())
		}
		require.Equal(t, 1, gets, "all preloaded lookups must hit the cache")
	})

	t.Run("replacement tokens", func(t *testing.T) {
		for _, newerFirst := range []bool{false, true} {
			c := newRedisCache("", 10, time.Minute, 0)
			c.trackingReady = true
			entries := []*Entry{{Inode: 1}}
			old := c.startPreload(entries)
			c.invalidateInode(1)
			newer := c.startPreload(entries)
			require.NotEqual(t, old, newer)
			if newerFirst {
				c.finishPreload(entries, newer, []interface{}{"new"})
			}
			c.finishPreload(entries, old, []interface{}{"old"})
			if !newerFirst {
				require.Equal(t, newer, c.preloadPending[1], "old completion must not clear a newer token")
				c.finishPreload(entries, newer, []interface{}{"new"})
			}
			data, ok := c.inodeCache.Get(1)
			require.True(t, ok)
			require.Equal(t, []byte("new"), data)
			require.Empty(t, c.preloadPending)
		}
	})

	t.Run("local SET invalidates pending preload", func(t *testing.T) {
		c := newRedisCache("", 10, time.Minute, 0)
		c.trackingReady = true
		entries := []*Entry{{Inode: 1}}
		token := c.startPreload(entries)
		cmd := redis.NewStatusCmd(context.Background(), "set", "i1", "new")
		cmd.SetVal("OK")
		c.afterProcess(cmd)
		c.finishPreload(entries, token, []interface{}{"old"})
		_, ok := c.inodeCache.Get(1)
		require.False(t, ok)
		require.Empty(t, c.preloadPending)
	})

	t.Run("tracking must be ready", func(t *testing.T) {
		c := newRedisCache("", 10, time.Minute, 0)
		c.trackingReady = true
		entries := []*Entry{{Inode: 1}}
		old := c.startPreload(entries)
		cli := redis.NewClient(&redis.Options{})
		defer cli.Close()
		fail := true
		cli.AddHook(redisCacheTestHook{process: func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
			require.Zero(t, c.startPreload(entries), "tracking is not enabled yet")
			c.finishPreload(entries, old, []interface{}{"old"})
			_, ok := c.inodeCache.Get(1)
			require.False(t, ok)
			if fail {
				err := errors.New("tracking unavailable")
				cmd.SetErr(err)
				return err
			}
			cmd.(*redis.Cmd).SetVal("OK")
			return nil
		}})
		cn := cli.Conn()
		defer cn.Close()
		require.Error(t, c.onInvalidateConnect(context.Background(), cn))
		require.Zero(t, c.startPreload(entries))
		fail = false
		require.NoError(t, c.onInvalidateConnect(context.Background(), cn))
		token := c.startPreload(entries)
		require.NotZero(t, token)
		c.finishPreload(entries, token, []interface{}{"new"})
		data, ok := c.inodeCache.Get(1)
		require.True(t, ok)
		require.Equal(t, []byte("new"), data)
		require.Empty(t, c.preloadPending)
	})

}
