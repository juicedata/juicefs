/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package sync

import (
	"bytes"
	"math"
	"os"
	"reflect"
	gosync "sync"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
)

func TestCheckpointManagerSaveAndLoad(t *testing.T) {
	srcStore, _ := object.CreateStorage("mem", "src", "", "", "")
	store, _ := object.CreateStorage("mem", "", "", "", "")
	if err := store.Put(ctx, "pending", bytes.NewReader([]byte("pending"))); err != nil {
		t.Fatalf("put pending object: %v", err)
	}
	pendingObj, err := store.Head(ctx, "pending")
	if err != nil {
		t.Fatalf("head pending object: %v", err)
	}

	manager := NewCheckpointManager(srcStore, store, nil)
	manager.statsUpdater = func(stats *CheckpointStats) {
		stats.Handled = 9
	}

	ckpt := &Checkpoint{
		PrefixState: map[string]*PrefixState{
			"prefix/": {
				ListDone:      true,
				LastListedKey: "pending",
				ListDepth:     1,
				PendingKeys: map[string]object.Object{
					"pending": pendingObj,
				},
				FailedKeys: make(map[string]object.Object),
			},
		},
		Config: &Config{
			Start:     "a",
			End:       "z",
			Include:   []string{"a*"},
			Exclude:   []string{"b*"},
			DeleteDst: true,
		},
		Stats: CheckpointStats{
			Copied: 3,
		},
	}

	beforeSave := time.Now()
	if err := manager.Save(ckpt); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if ckpt.UpdatedAt.Before(beforeSave) {
		t.Fatalf("checkpoint UpdatedAt was not refreshed: %s", ckpt.UpdatedAt)
	}

	loader := NewCheckpointManager(srcStore, store, nil)
	loaded, err := loader.Load()
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}

	if loaded.Stats.Copied != 3 || loaded.Stats.Handled != 9 {
		t.Fatalf("unexpected checkpoint stats: %+v", loaded.Stats)
	}
	if loaded.Config == nil || loaded.Config.Start != "a" || loaded.Config.End != "z" || !loaded.Config.DeleteDst {
		t.Fatalf("unexpected loaded config: %+v", loaded.Config)
	}
	if !reflect.DeepEqual(loaded.Config.Include, []string{"a*"}) || !reflect.DeepEqual(loaded.Config.Exclude, []string{"b*"}) {
		t.Fatalf("unexpected loaded filters: include=%v exclude=%v", loaded.Config.Include, loaded.Config.Exclude)
	}
	if loader.checkpoint != loaded {
		t.Fatalf("checkpoint manager should keep loaded checkpoint")
	}

	state := loaded.PrefixState["prefix/"]
	if state == nil || !state.ListDone || state.LastListedKey != "pending" || state.ListDepth != 1 {
		t.Fatalf("unexpected loaded prefix state: %+v", state)
	}
	if len(state.PendingKeys) != 1 || state.PendingKeys["pending"] == nil {
		t.Fatalf("unexpected loaded pending keys: %+v", state.PendingKeys)
	}
}

func TestCheckpointManagerPrefixStateLifecycle(t *testing.T) {
	srcStore, _ := object.CreateStorage("mem", "src", "", "", "")
	store, _ := object.CreateStorage("mem", "", "", "", "")
	if err := store.Put(ctx, "prefix/file", bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("put object: %v", err)
	}
	obj, err := store.Head(ctx, "prefix/file")
	if err != nil {
		t.Fatalf("head object: %v", err)
	}

	manager := NewCheckpointManager(srcStore, store, nil)
	manager.checkpoint = &Checkpoint{
		PrefixState: make(map[string]*PrefixState),
	}

	manager.AddPendingKey("prefix/", obj)

	state := manager.checkpoint.PrefixState["prefix/"]
	if state == nil {
		t.Fatalf("prefix state should be created")
	}
	if state.LastListedKey != "prefix/file" {
		t.Fatalf("unexpected last listed key: %s", state.LastListedKey)
	}
	if len(state.PendingKeys) != 1 || state.PendingKeys["prefix/file"] == nil {
		t.Fatalf("unexpected pending keys after AddPendingKey: %+v", state.PendingKeys)
	}
	prefix, ok := manager.keyPrefix.Load("prefix/file")
	if !ok || prefix.(string) != "prefix/" {
		t.Fatalf("unexpected key prefix mapping: %v %v", prefix, ok)
	}

	manager.MarkFailed("prefix/file")

	state = manager.checkpoint.PrefixState["prefix/"]
	if len(state.PendingKeys) != 0 || len(state.FailedKeys) != 1 || state.FailedKeys["prefix/file"] == nil {
		t.Fatalf("unexpected state after MarkFailed: pending=%v failed=%v", state.PendingKeys, state.FailedKeys)
	}

	manager.MarkCompleted("prefix/file")

	state = manager.checkpoint.PrefixState["prefix/"]
	if len(state.PendingKeys) != 0 || len(state.FailedKeys) != 0 {
		t.Fatalf("unexpected state after MarkCompleted: pending=%v failed=%v", state.PendingKeys, state.FailedKeys)
	}
	if _, ok := manager.keyPrefix.Load("prefix/file"); ok {
		t.Fatalf("key prefix mapping should be removed after completion")
	}

	manager.MarkListDone("prefix/")
	if _, ok := manager.checkpoint.PrefixState["prefix/"]; ok {
		t.Fatalf("empty completed prefix state should be removed")
	}
}

func TestCheckpointManagerValidateConfig(t *testing.T) {
	base := &Config{
		Start:          "a",
		End:            "z",
		Include:        []string{"a*"},
		Exclude:        []string{"b*"},
		DeleteSrc:      true,
		DeleteDst:      true,
		Update:         true,
		ForceUpdate:    true,
		Existing:       true,
		IgnoreExisting: true,
		Links:          true,
		CheckAll:       true,
		CheckNew:       true,
		CheckChange:    true,
		Perms:          true,
		Dirs:           true,
		MaxSize:        100,
		MinSize:        10,
		MaxAge:         2 * time.Hour,
		MinAge:         time.Minute,
	}
	manager := &CheckpointManager{
		checkpoint: &Checkpoint{Config: base},
	}

	testCases := []struct {
		name   string
		mutate func(*Config)
		want   bool
	}{
		{
			name: "match",
			want: true,
		},
		{
			name: "start_end",
			mutate: func(c *Config) {
				c.Start = "b"
			},
			want: false,
		},
		{
			name: "include_exclude",
			mutate: func(c *Config) {
				c.Include = []string{"c*"}
			},
			want: false,
		},
		{
			name: "delete_options",
			mutate: func(c *Config) {
				c.DeleteDst = false
			},
			want: false,
		},
		{
			name: "update_strategy",
			mutate: func(c *Config) {
				c.ForceUpdate = false
			},
			want: false,
		},
		{
			name: "check_options",
			mutate: func(c *Config) {
				c.CheckChange = false
			},
			want: false,
		},
		{
			name: "perms_dirs",
			mutate: func(c *Config) {
				c.Dirs = false
			},
			want: false,
		},
		{
			name: "size_age_filters",
			mutate: func(c *Config) {
				c.MaxAge = time.Hour
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		current := *base
		current.Include = append([]string(nil), base.Include...)
		current.Exclude = append([]string(nil), base.Exclude...)
		if tc.mutate != nil {
			tc.mutate(&current)
		}
		if got := manager.ValidateConfig(&current); got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// newTestConfig returns a Config suitable for mem-to-mem checkpoint tests.
func newTestConfig() *Config {
	return &Config{
		Threads:            4,
		ListThreads:        2,
		ListDepth:          2,
		Update:             true,
		Limit:              -1,
		MaxSize:            math.MaxInt64,
		Quiet:              true,
		EnableCheckpoint:   true,
		CheckpointInterval: time.Minute,
	}
}

// seedCheckpoint creates a checkpoint in dst that simulates an interrupted sync:
//   - "dir/" listing not done, last listed key "dir/a"
//   - "dir/sub/" listing not done, last listed key "dir/sub/x",
//     with "dir/sub/pending" as pending and "dir/sub/y" as failed
func seedCheckpoint(t *testing.T, src, dst object.ObjectStorage, config *Config) {
	t.Helper()
	pendingObj, err := src.Head(ctx, "dir/sub/pending")
	if err != nil {
		t.Fatalf("head dir/sub/pending: %v", err)
	}
	failedObj, err := src.Head(ctx, "dir/sub/y")
	if err != nil {
		t.Fatalf("head dir/sub/y: %v", err)
	}

	mgr := NewCheckpointManager(src, dst, config)
	ckpt := &Checkpoint{
		PrefixState: map[string]*PrefixState{
			"dir/": {
				ListDone:      false,
				LastListedKey: "dir/a",
				ListDepth:     2,
				PendingKeys:   make(map[string]object.Object),
				FailedKeys:    make(map[string]object.Object),
			},
			"dir/sub/": {
				ListDone:      false,
				LastListedKey: "dir/sub/x",
				ListDepth:     1,
				PendingKeys:   map[string]object.Object{"dir/sub/pending": pendingObj},
				FailedKeys:    map[string]object.Object{"dir/sub/y": failedObj},
			},
		},
		Config: config,
	}
	if err := mgr.Save(ckpt); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
}

func putObjects(t *testing.T, store object.ObjectStorage, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if err := store.Put(ctx, key, bytes.NewReader([]byte(key))); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
}

func collectTasks(tasks <-chan object.Object) []string {
	var keys []string
	for obj := range tasks {
		keys = append(keys, obj.Key())
	}
	return keys
}

func assertDstHasKeys(t *testing.T, dst object.ObjectStorage, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, err := dst.Head(ctx, key); err != nil {
			t.Errorf("expected dst to have %q, but Head returned: %v", key, err)
		}
	}
}

// TestRestoreFromCheckpointChildPrefixKeys tests the restoreFromCheckpoint path:
// checkpoint has "dir/" (not done) and "dir/sub/" (with pending/failed keys).
// Verifies that child prefix pending/failed keys are properly restored and synced.
func TestRestoreFromCheckpointChildPrefixKeys(t *testing.T) {
	src, _ := object.CreateStorage("mem", "ckpt-restore-src", "", "", "")
	dst, _ := object.CreateStorage("mem", "ckpt-restore-dst", "", "", "")

	putObjects(t, src, "dir/a", "dir/b", "dir/sub/x", "dir/sub/y", "dir/sub/pending")

	config := newTestConfig()
	seedCheckpoint(t, src, dst, config)

	if err := Sync(src, dst, config); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// dir/sub/'s pending and failed keys must be synced to dst
	assertDstHasKeys(t, dst, "dir/sub/y", "dir/sub/pending")
}

// TestProduceFromListChildPrefixKeys tests the produceFromList path:
// files-from has only "dir/", checkpoint has "dir/" and "dir/sub/" (with pending/failed keys).
// Verifies that child prefix pending/failed keys are not lost.
func TestProduceFromListChildPrefixKeys(t *testing.T) {
	src, _ := object.CreateStorage("mem", "ckpt-produce-src", "", "", "")
	dst, _ := object.CreateStorage("mem", "ckpt-produce-dst", "", "", "")

	putObjects(t, src, "dir/a", "dir/b", "dir/sub/x", "dir/sub/y", "dir/sub/pending")

	// Write a files-from file with just "dir/"
	tmpFile, err := os.CreateTemp("", "files-from-*.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString("dir/\n"); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	tmpFile.Close()

	config := newTestConfig()
	config.FilesFrom = tmpFile.Name()
	seedCheckpoint(t, src, dst, config)

	if err := Sync(src, dst, config); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// dir/sub/'s pending and failed keys must be synced to dst
	assertDstHasKeys(t, dst, "dir/sub/y", "dir/sub/pending")
}

// TestRestorePrefixDedup verifies that concurrent calls to restorePrefixFromCheckpoint
// for the same prefix only restore pending/failed keys once.
func TestRestorePrefixDedup(t *testing.T) {
	src, _ := object.CreateStorage("mem", "dedup-src", "", "", "")
	dst, _ := object.CreateStorage("mem", "dedup-dst", "", "", "")

	putObjects(t, src, "p/file1")
	obj, _ := src.Head(ctx, "p/file1")

	mgr := NewCheckpointManager(src, dst, nil)
	mgr.checkpoint = &Checkpoint{
		PrefixState: map[string]*PrefixState{
			"p/": {
				ListDone:    true,
				ListDepth:   1,
				PendingKeys: map[string]object.Object{"p/file1": obj},
				FailedKeys:  make(map[string]object.Object),
			},
		},
	}

	tasks := make(chan object.Object, 100)
	config := newTestConfig()
	config.concurrentList = make(chan int, config.ListThreads)

	// Call concurrently — only one should actually restore
	var wg gosync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			restorePrefixFromCheckpoint(tasks, src, dst, "p/", config, mgr)
		}()
	}
	wg.Wait()
	close(tasks)

	keys := collectTasks(tasks)
	if len(keys) != 1 || keys[0] != "p/file1" {
		t.Fatalf("expected exactly 1 task [p/file1], got %d: %v", len(keys), keys)
	}
}

// TestRestoreListDonePrefix verifies that a prefix with ListDone=true restores
// pending/failed keys but does NOT restart listing.
func TestRestoreListDonePrefix(t *testing.T) {
	src, _ := object.CreateStorage("mem", "listdone-src", "", "", "")
	dst, _ := object.CreateStorage("mem", "listdone-dst", "", "", "")

	putObjects(t, src, "dir/a", "dir/b", "dir/sub/x", "dir/sub/pending")
	pendingObj, _ := src.Head(ctx, "dir/sub/pending")

	config := newTestConfig()

	mgr := NewCheckpointManager(src, dst, config)
	ckpt := &Checkpoint{
		PrefixState: map[string]*PrefixState{
			"dir/": {
				ListDone:    true,
				ListDepth:   2,
				PendingKeys: make(map[string]object.Object),
				FailedKeys:  make(map[string]object.Object),
			},
			"dir/sub/": {
				ListDone:    true,
				ListDepth:   1,
				PendingKeys: map[string]object.Object{"dir/sub/pending": pendingObj},
				FailedKeys:  make(map[string]object.Object),
			},
		},
		Config: config,
	}
	if err := mgr.Save(ckpt); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	Sync(src, dst, config)

	// Only the pending key should be synced
	assertDstHasKeys(t, dst, "dir/sub/pending")

	// ListDone=true means no new listing — these should NOT be in dst
	for _, key := range []string{"dir/a", "dir/b", "dir/sub/x"} {
		if _, err := dst.Head(ctx, key); err == nil {
			t.Errorf("dst should NOT have %q (listing should not have restarted)", key)
		}
	}
}

func TestSyncCheckpointForceResetStartsFresh(t *testing.T) {
	src, _ := object.CreateStorage("mem", "force-reset-src", "", "", "")
	dst, _ := object.CreateStorage("mem", "force-reset-dst", "", "", "")

	putObjects(t, src, "dir/a", "dir/b", "dir/sub/x", "dir/sub/pending")
	pendingObj, err := src.Head(ctx, "dir/sub/pending")
	if err != nil {
		t.Fatalf("head pending object: %v", err)
	}

	seedConfig := newTestConfig()
	mgr := NewCheckpointManager(src, dst, seedConfig)
	ckpt := &Checkpoint{
		PrefixState: map[string]*PrefixState{
			"dir/": {
				ListDone:    true,
				ListDepth:   2,
				PendingKeys: make(map[string]object.Object),
				FailedKeys:  make(map[string]object.Object),
			},
			"dir/sub/": {
				ListDone:    true,
				ListDepth:   1,
				PendingKeys: map[string]object.Object{"dir/sub/pending": pendingObj},
				FailedKeys:  make(map[string]object.Object),
			},
		},
		Config: seedConfig,
	}
	if err := mgr.Save(ckpt); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	config := newTestConfig()
	config.CheckpointForceReset = true
	if err := Sync(src, dst, config); err != nil {
		t.Fatalf("Sync with checkpoint force reset: %v", err)
	}

	assertDstHasKeys(t, dst, "dir/a", "dir/b", "dir/sub/x", "dir/sub/pending")
	if _, err := dst.Head(ctx, generateCheckpointKey(src.String(), dst.String(), config)); !os.IsNotExist(err) {
		t.Fatalf("checkpoint file should be removed after successful force-reset sync, got err=%v", err)
	}
}

// TestProduceFromListOverlappingPrefixes verifies that when files-from contains
// both "dir/" and "dir/sub/", and both are in checkpoint, each is restored exactly once.
func TestProduceFromListOverlappingPrefixes(t *testing.T) {
	src, _ := object.CreateStorage("mem", "overlap-src", "", "", "")
	dst, _ := object.CreateStorage("mem", "overlap-dst", "", "", "")

	putObjects(t, src, "dir/a", "dir/b", "dir/sub/x", "dir/sub/y", "dir/sub/pending")

	tmpFile, err := os.CreateTemp("", "files-from-overlap-*.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString("dir/\ndir/sub/\n"); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	tmpFile.Close()

	config := newTestConfig()
	config.FilesFrom = tmpFile.Name()
	seedCheckpoint(t, src, dst, config)

	if err := Sync(src, dst, config); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	assertDstHasKeys(t, dst, "dir/sub/y", "dir/sub/pending")
}
