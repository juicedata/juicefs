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
	"reflect"
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
	ckpt := &Checkpoint{Config: base}
	manager := &CheckpointManager{}

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
		if got := manager.ValidateConfig(ckpt, &current); got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
