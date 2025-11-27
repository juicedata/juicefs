//go:build !noredis
// +build !noredis

/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package meta

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/push"
)

var entryMark cachedEntry

type cachedEntry struct {
	ino Ino
	Attr
}

func (e *cachedEntry) isMark() bool {
	return e.ino == 0
}

// redisCache support bcast mode client-side cache
// cache attrs and entries only, chunks are already cached in OpenCache
type redisCache struct {
	cli          *redis.Client
	prefix       string
	cap          int
	expiry       time.Duration
	preload      int
	subscription *redis.PubSub

	inodeCache *expirable.LRU[Ino, []byte]
	entryCache *expirable.LRU[string, *cachedEntry]
}

func newRedisCache(prefix string, cap int, expiry time.Duration, preload int) *redisCache {
	logger.Infof("Initializing Redis client-side cache with size %d and expiry %+v", cap, expiry)
	return &redisCache{
		prefix:     prefix,
		cap:        cap,
		expiry:     expiry,
		preload:    preload,
		inodeCache: expirable.NewLRU[Ino, []byte](cap, nil, expiry),
		entryCache: expirable.NewLRU[string, *cachedEntry](cap, nil, expiry),
	}
}

func (c *redisCache) init(cli redis.UniversalClient) error {
	ctx := context.WithValue(context.Background(), invalidConnKey{}, true)
	var err error
	if rc, ok := cli.(*redis.Client); ok {
		c.cli = rc
	} else if cc, ok := cli.(*redis.ClusterClient); ok {
		// For cluster mode, we should get the master node for our key
		if c.cli, err = cc.MasterForKey(ctx, c.prefix); err != nil {
			return err
		}
	}
	c.cli.Options().OnConnect = c.onInvalidateConnect
	// under the RESP3 protocol, "__redis__:invalidate" actually has no effect.
	// we use Pubsub channel to simplify connection management and receiving PUSH messages.
	c.subscription = c.cli.Subscribe(ctx, "__redis__:invalidate")
	_ = c.subscription.Channel()
	// handle PUSH notifications for invalidation in c.HandlePushNotification
	if err = c.cli.RegisterPushNotificationHandler("invalidate", c, true); err != nil {
		c.close()
		return err
	}
	// handle client cmd to avoid race conditions
	c.cli.AddHook(c)
	return nil
}

const (
	keyTypOther = iota
	keyTypInode
	keyTypEntry
)

func (c *redisCache) parse(key string) int {
	if strings.HasPrefix(key, c.prefix+"i") {
		return keyTypInode
	}
	if strings.HasPrefix(key, c.prefix+"d") {
		return keyTypEntry
	}
	return keyTypOther
}

func (c *redisCache) entryName(parent Ino, name string) string {
	return fmt.Sprintf("%d%d%s", parent, os.PathSeparator, name)
}

func (c *redisCache) HandlePushNotification(ctx context.Context, handlerCtx push.NotificationHandlerContext, notification []interface{}) error {
	if len(notification) != 2 || notification[0] == nil || notification[1] == nil {
		return nil
	}
	if typ, ok := notification[0].(string); !ok || typ != "invalidate" {
		return nil
	}
	iKeys := notification[1].([]interface{})
	var key string
	for _, iKey := range iKeys {
		key = iKey.(string)
		typ := c.parse(key)
		switch typ {
		case keyTypInode:
			inodeStr := key[len(c.prefix)+1:]
			inode, err := strconv.ParseUint(inodeStr, 10, 64)
			if err == nil {
				c.inodeCache.Remove(Ino(inode))
			}
		case keyTypEntry:
			parentStr := key[len(c.prefix)+1:]
			// invalidate all entries related to this directory
			prefix := fmt.Sprintf("%s%d", parentStr, os.PathSeparator)
			for _, k := range c.entryCache.Keys() {
				if strings.HasPrefix(k, prefix) {
					c.entryCache.Remove(k)
				}
			}
		}
	}
	return nil
}

func (c *redisCache) DialHook(next redis.DialHook) redis.DialHook { return nil }

var inodeMark []byte

func (c *redisCache) beforeProcess(cmd redis.Cmder, skip bool) bool {
	name, args := cmd.Name(), cmd.Args()
	var key string
	var ok bool
	if len(args) < 2 {
		return true
	}
	if key, ok = args[1].(string); !ok {
		return true
	}
	typ := c.parse(key)

	if name == "get" && typ == keyTypInode {
		num, err := strconv.ParseUint(key[len(c.prefix)+1:], 10, 64)
		if err == nil {
			inode := Ino(num)
			if data, ok := c.inodeCache.Get(inode); ok {
				if !skip && len(data) > 0 {
					rsp := cmd.(*redis.StringCmd)
					rsp.SetErr(nil)
					rsp.SetVal(bytesToString(data))
					return false
				}
			}
			c.inodeCache.AddIf(inode, inodeMark, func(oldVal []byte, exists bool) bool {
				return !exists
			})
			// request to Redis server
		}
	}
	return true
}

func (c *redisCache) afterProcess(cmd redis.Cmder) {
	name, args := cmd.Name(), cmd.Args()
	var key string
	var ok bool
	if len(args) < 2 {
		return
	}
	if key, ok = args[1].(string); !ok {
		return
	}
	typ := c.parse(key)

	switch name {
	case "get":
		if typ == keyTypInode {
			if data, err := cmd.(*redis.StringCmd).Bytes(); err == nil {
				num, err := strconv.ParseUint(key[len(c.prefix)+1:], 10, 64)
				if err != nil {
					return
				}
				_, _ = c.inodeCache.AddIf(Ino(num), data, func(oldVal []byte, exists bool) bool {
					return exists && len(oldVal) == 0
				})
			}
		}
	case "set":
		if typ == keyTypInode {
			if cmd.(*redis.StatusCmd).Err() == nil {
				if num, err := strconv.ParseUint(key[len(c.prefix)+1:], 10, 64); err == nil {
					_ = c.inodeCache.Remove(Ino(num))
				}
			}
		}
	case "hdel":
		if typ == keyTypEntry {
			if err := cmd.(*redis.IntCmd).Err(); err == nil {
				for i := 2; i < len(args); i++ {
					_ = c.entryCache.Remove(fmt.Sprintf("%s%d%s", key[len(c.prefix)+1:], os.PathSeparator, args[i]))
				}
			}
		}
	case "hset":
		if typ == keyTypEntry {
			if err := cmd.(*redis.IntCmd).Err(); err == nil {
				for i := 2; i < len(args); i += 2 {
					_ = c.entryCache.Remove(fmt.Sprintf("%s%d%s", key[len(c.prefix)+1:], os.PathSeparator, args[i]))
				}
			}
		}
	}
}

func (c *redisCache) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if !c.beforeProcess(cmd, false) {
			return nil
		}
		err := next(ctx, cmd)
		c.afterProcess(cmd)
		return err
	}
}

func (c *redisCache) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			_ = c.beforeProcess(cmd, true)
		}
		err := next(ctx, cmds)
		for _, cmd := range cmds {
			c.afterProcess(cmd)
		}
		return err
	}
}

func (c *redisCache) close() {
	if c.subscription != nil {
		if err := c.subscription.Close(); err != nil {
			logger.Warnf("failed closing Redis cache subscription: %v", err)
		}
		c.subscription = nil
	}
	c.cli.Options().OnConnect = nil
	c.cli = nil
}

type invalidConnKey struct{}

func (c *redisCache) onInvalidateConnect(ctx context.Context, cn *redis.Conn) error {
	if ctx.Value(invalidConnKey{}) == nil {
		return nil
	}
	// clear all caches on reconnect
	c.inodeCache.Purge()
	c.entryCache.Purge()
	// use the pubsub connection to handle tracking and invalidate
	_ = cn.Do(ctx, "CLIENT", "TRACKING", "OFF").Err()
	if err := cn.Do(ctx, "CLIENT", "TRACKING", "ON", "BCAST", "PREFIX", c.prefix+"i", "PREFIX", c.prefix+"d").Err(); err != nil {
		logger.Warnf("Failed to enable Redis client-side caching on new connection: %v", err)
		return err
	}
	return nil
}

func (m *redisMeta) preloadCache() {
	if m.cache == nil {
		return
	}
	if m.cache.preload <= 0 {
		return
	}
	start := time.Now()
	ctx := Background()
	attr := &Attr{}
	if eno := m.doGetAttr(ctx, m.root, attr); eno != 0 {
		logger.Warnf("failed to get root inode %d attribute: %d", m.root, eno)
		return
	}

	var entries []*Entry
	if eno := m.doReaddir(ctx, m.root, 1, &entries, m.cache.preload); eno != 0 {
		logger.Warnf("failed to read root %d directory: %d", m.root, eno)
		return
	}
	for _, entry := range entries {
		m.cache.entryCache.Add(m.cache.entryName(m.root, string(entry.Name)), &cachedEntry{
			ino:  entry.Inode,
			Attr: *entry.Attr,
		})
	}
	logger.Infof("preload %d inodes in %v", m.cache.inodeCache.Len(), time.Since(start))
}

func bytesToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
