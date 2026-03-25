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
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
)

const (
	checkpointPrefix = ".juicefs-sync-checkpoint"
)

// PrefixState maintains the state for a specific prefix
type PrefixState struct {
	sync.RWMutex
	ListDone      bool                     `json:"list_done"`
	LastListedKey string                   `json:"last_listed_key"`
	ListDepth     int                      `json:"list_depth"`
	PendingKeys   map[string]object.Object `json:"-"`
	FailedKeys    map[string]object.Object `json:"-"`
}

type prefixStateJSON struct {
	ListDone      bool                              `json:"list_done"`
	LastListedKey string                            `json:"last_listed_key"`
	ListDepth     int                               `json:"list_depth"`
	PendingKeys   map[string]map[string]interface{} `json:"pending_keys"`
	FailedKeys    map[string]map[string]interface{} `json:"failed_keys"`
}

func (s *PrefixState) MarshalJSON() ([]byte, error) {
	j := prefixStateJSON{
		ListDone:      s.ListDone,
		LastListedKey: s.LastListedKey,
		ListDepth:     s.ListDepth,
		PendingKeys:   make(map[string]map[string]interface{}, len(s.PendingKeys)),
		FailedKeys:    make(map[string]map[string]interface{}, len(s.FailedKeys)),
	}
	for k, obj := range s.PendingKeys {
		j.PendingKeys[k] = object.MarshalObject(obj)
	}
	for k, obj := range s.FailedKeys {
		j.FailedKeys[k] = object.MarshalObject(obj)
	}
	return json.Marshal(j)
}

func (s *PrefixState) UnmarshalJSON(data []byte) error {
	var j prefixStateJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	s.ListDone = j.ListDone
	s.LastListedKey = j.LastListedKey
	s.ListDepth = j.ListDepth
	s.PendingKeys = make(map[string]object.Object, len(j.PendingKeys))
	for k, m := range j.PendingKeys {
		if obj := object.UnmarshalObject(m); obj != nil {
			s.PendingKeys[k] = obj
		}
	}
	s.FailedKeys = make(map[string]object.Object, len(j.FailedKeys))
	for k, m := range j.FailedKeys {
		if obj := object.UnmarshalObject(m); obj != nil {
			s.FailedKeys[k] = obj
		}
	}
	return nil
}

// CheckpointStats stores cumulative statistics
type CheckpointStats struct {
	Copied       int64 `json:"copied"`
	CopiedBytes  int64 `json:"copied_bytes"`
	Checked      int64 `json:"checked"`
	CheckedBytes int64 `json:"checked_bytes"`
	Deleted      int64 `json:"deleted"`
	Skipped      int64 `json:"skipped"`
	SkippedBytes int64 `json:"skipped_bytes"`
	Failed       int64 `json:"failed"`
	Handled      int64 `json:"handled"`
}

// Checkpoint represents the complete checkpoint state
type Checkpoint struct {
	sync.RWMutex
	PrefixState map[string]*PrefixState `json:"prefix_state"`
	Config      *Config                 `json:"config"`
	Stats       CheckpointStats         `json:"stats"`
	SrcDelayDel []string                `json:"src_delay_del,omitempty"`
	DstDelayDel []string                `json:"dst_delay_del,omitempty"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

// CheckpointManager manages checkpoint persistence
type CheckpointManager struct {
	saveMu           sync.Mutex
	dst              object.ObjectStorage
	checkpoint       *Checkpoint
	checkpointKey    string
	stopChan         chan struct{}
	keyPrefix        sync.Map               // key -> prefix mapping for fast lookup
	statsUpdater     func(*CheckpointStats) // callback to update stats before save
	restoredPrefixes sync.Map               // for dedup: prefixes already restored
}

func newCheckpoint(config *Config) *Checkpoint {
	return &Checkpoint{
		PrefixState: make(map[string]*PrefixState),
		Config:      config,
		UpdatedAt:   time.Now(),
	}
}

// NewCheckpointManager creates a new checkpoint manager
func NewCheckpointManager(src, dst object.ObjectStorage, config *Config) *CheckpointManager {
	key := generateCheckpointKey(src.String(), dst.String())
	return &CheckpointManager{
		dst:           dst,
		checkpoint:    newCheckpoint(config),
		checkpointKey: key,
		stopChan:      make(chan struct{}),
	}
}

// generateCheckpointKey creates a unique key based on src and dst
func generateCheckpointKey(src, dst string) string {
	hash := md5.Sum([]byte(src + "|" + dst))
	if strings.HasSuffix(dst, "/") {
		return fmt.Sprintf("%s.%x.json", checkpointPrefix, hash)
	}
	return fmt.Sprintf("/%s.%x.json", checkpointPrefix, hash)
}

func (m *CheckpointManager) isCheckpointKey(key string) bool {
	return m != nil && key == m.checkpointKey
}

// Load loads checkpoint from object storage
func (m *CheckpointManager) Load() (*Checkpoint, error) {
	obj, err := m.dst.Get(ctx, m.checkpointKey, 0, -1)
	if err != nil {
		return nil, fmt.Errorf("failed to get checkpoint: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}

	var ckpt Checkpoint
	if err := json.Unmarshal(data, &ckpt); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint: %w", err)
	}

	m.checkpoint = &ckpt
	return &ckpt, nil
}

// Save saves checkpoint to object storage
func (m *CheckpointManager) Save(ckpt *Checkpoint) error {
	if ckpt.Config != nil && ckpt.Config.Dry {
		return nil
	}
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	if m.statsUpdater != nil {
		m.statsUpdater(&ckpt.Stats)
	}

	srcDelayDelMu.Lock()
	ckpt.SrcDelayDel = append([]string(nil), srcDelayDel...)
	srcDelayDelMu.Unlock()
	dstDelayDelMu.Lock()
	ckpt.DstDelayDel = append([]string(nil), dstDelayDel...)
	dstDelayDelMu.Unlock()

	ckpt.UpdatedAt = time.Now()

	ckpt.RLock()
	prefixCount := len(ckpt.PrefixState)
	for _, state := range ckpt.PrefixState {
		state.RLock()
	}
	data, err := json.Marshal(ckpt)
	for _, state := range ckpt.PrefixState {
		state.RUnlock()
	}
	ckpt.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}

	logger.Debugf("Saving checkpoint with %d prefixes, copied: %d, failed: %d",
		prefixCount, ckpt.Stats.Copied, ckpt.Stats.Failed)
	reader := bytes.NewReader(data)
	if err := m.dst.Put(ctx, m.checkpointKey, reader); err != nil {
		return fmt.Errorf("failed to put checkpoint: %w", err)
	}

	return nil
}

// ValidateConfig checks if checkpoint config matches current config
func (m *CheckpointManager) ValidateConfig(ckpt *Checkpoint, current *Config) bool {
	c := ckpt.Config

	// Must match fields
	if c.Start != current.Start || c.End != current.End {
		logger.Warnf("Checkpoint config mismatch: start/end")
		return false
	}

	if !stringSliceEqual(c.Include, current.Include) || !stringSliceEqual(c.Exclude, current.Exclude) {
		logger.Warnf("Checkpoint config mismatch: include/exclude")
		return false
	}

	if c.DeleteSrc != current.DeleteSrc || c.DeleteDst != current.DeleteDst {
		logger.Warnf("Checkpoint config mismatch: delete options")
		return false
	}

	if c.Update != current.Update || c.ForceUpdate != current.ForceUpdate ||
		c.Existing != current.Existing || c.IgnoreExisting != current.IgnoreExisting {
		logger.Warnf("Checkpoint config mismatch: update strategy")
		return false
	}

	if c.Links != current.Links || c.CheckAll != current.CheckAll ||
		c.CheckNew != current.CheckNew || c.CheckChange != current.CheckChange {
		logger.Warnf("Checkpoint config mismatch: check options")
		return false
	}

	if c.Perms != current.Perms || c.Dirs != current.Dirs {
		logger.Warnf("Checkpoint config mismatch: perms/dirs")
		return false
	}

	if c.MaxSize != current.MaxSize || c.MinSize != current.MinSize ||
		c.MaxAge != current.MaxAge || c.MinAge != current.MinAge {
		logger.Warnf("Checkpoint config mismatch: size/age filters")
		return false
	}

	if c.FilesFrom != current.FilesFrom {
		logger.Warnf("Checkpoint config mismatch: files-from")
		return false
	}

	if c.MatchFullPath != current.MatchFullPath {
		logger.Warnf("Checkpoint config mismatch: match-full-path")
		return false
	}

	if c.StartTime != current.StartTime || c.EndTime != current.EndTime {
		logger.Warnf("Checkpoint config mismatch: time filters")
		return false
	}

	return true
}

// GetOrCreatePrefixState gets or creates a prefix state
func (m *CheckpointManager) GetOrCreatePrefixState(prefix string) *PrefixState {
	m.checkpoint.RLock()
	state, exists := m.checkpoint.PrefixState[prefix]
	m.checkpoint.RUnlock()
	if exists {
		return state
	}

	m.checkpoint.Lock()
	defer m.checkpoint.Unlock()
	if state, exists = m.checkpoint.PrefixState[prefix]; exists {
		return state
	}
	state = &PrefixState{
		PendingKeys: make(map[string]object.Object),
		FailedKeys:  make(map[string]object.Object),
	}
	m.checkpoint.PrefixState[prefix] = state
	return state
}

func (m *CheckpointManager) updatePrefixState(prefix string, update func(*PrefixState)) {
	state := m.GetOrCreatePrefixState(prefix)

	state.Lock()
	update(state)
	done := state.ListDone && len(state.PendingKeys) == 0 && len(state.FailedKeys) == 0 &&
		(m.checkpoint.Config == nil || m.checkpoint.Config.FilesFrom == "")
	state.Unlock()
	if done {
		m.checkpoint.Lock()
		delete(m.checkpoint.PrefixState, prefix)
		m.checkpoint.Unlock()
	}
}

func (m *CheckpointManager) MarkListDone(prefix string) {
	m.updatePrefixState(prefix, func(state *PrefixState) {
		state.ListDone = true
	})
}

// MarkCompleted removes a key from PendingKeys after successful completion
func (m *CheckpointManager) MarkCompleted(key string) {
	prefixVal, ok := m.keyPrefix.LoadAndDelete(key)
	if !ok {
		return
	}
	prefix := prefixVal.(string)

	m.updatePrefixState(prefix, func(state *PrefixState) {
		delete(state.PendingKeys, key)
		delete(state.FailedKeys, key)
	})
}

// MarkFailed moves a key from PendingKeys to FailedKeys
func (m *CheckpointManager) MarkFailed(key string) {
	prefixVal, ok := m.keyPrefix.Load(key)
	if !ok {
		return
	}
	prefix := prefixVal.(string)

	m.updatePrefixState(prefix, func(state *PrefixState) {
		objData, ok := state.PendingKeys[key]
		if !ok {
			return
		}
		delete(state.PendingKeys, key)
		state.FailedKeys[key] = objData
	})
}

func (m *CheckpointManager) AddPendingKey(prefix string, obj object.Object) {
	m.updatePrefixState(prefix, func(state *PrefixState) {
		state.PendingKeys[obj.Key()] = obj
		state.LastListedKey = obj.Key()
	})
	m.TrackKey(obj.Key(), prefix)
}

func (m *CheckpointManager) UpdateLastListedKey(prefix string, obj object.Object) {
	m.updatePrefixState(prefix, func(state *PrefixState) {
		state.LastListedKey = obj.Key()
	})
}

func (m *CheckpointManager) TrackKey(key, prefix string) {
	if m == nil {
		return
	}
	m.keyPrefix.Store(key, prefix)
}

// GetLastListedKey returns the last listed key for a prefix, or "" if not tracked.
func (m *CheckpointManager) GetLastListedKey(prefix string) string {
	if m == nil {
		return ""
	}
	m.checkpoint.RLock()
	state, exists := m.checkpoint.PrefixState[prefix]
	m.checkpoint.RUnlock()
	if !exists {
		return ""
	}
	state.RLock()
	defer state.RUnlock()
	return state.LastListedKey
}

// RestorePrefix restores pending+failed keys for a prefix, merging failed into pending.
func (m *CheckpointManager) RestorePrefix(prefix string) (objs []object.Object, listDone bool, listDepth int, found bool) {
	if m == nil {
		return nil, false, 0, false
	}
	m.checkpoint.RLock()
	state, exists := m.checkpoint.PrefixState[prefix]
	m.checkpoint.RUnlock()
	if !exists {
		return nil, false, 0, false
	}
	if _, loaded := m.restoredPrefixes.LoadOrStore(prefix, struct{}{}); loaded {
		return nil, false, 0, true
	}
	state.Lock()
	maps.Copy(state.PendingKeys, state.FailedKeys)
	state.FailedKeys = make(map[string]object.Object)
	objs = make([]object.Object, 0, len(state.PendingKeys))
	for key, obj := range state.PendingKeys {
		m.keyPrefix.Store(key, prefix)
		objs = append(objs, obj)
	}
	listDone = state.ListDone
	listDepth = state.ListDepth
	state.Unlock()
	return objs, listDone, listDepth, true
}

// ListPrefixes returns a snapshot of all prefix keys currently tracked in checkpoint.
func (m *CheckpointManager) ListPrefixes() []string {
	m.checkpoint.RLock()
	defer m.checkpoint.RUnlock()
	prefixes := make([]string, 0, len(m.checkpoint.PrefixState))
	for prefix := range m.checkpoint.PrefixState {
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

// RegisterChildPrefix registers a child prefix discovered during listing.
func (m *CheckpointManager) RegisterChildPrefix(childPrefix string, listDepth int) {
	if m == nil {
		return
	}
	state := m.GetOrCreatePrefixState(childPrefix)
	state.Lock()
	state.ListDepth = listDepth
	state.Unlock()
}

// DeleteCheckpoint removes the checkpoint file from storage.
func (m *CheckpointManager) DeleteCheckpoint() error {
	return m.dst.Delete(ctx, m.checkpointKey)
}

// Reset discards the current checkpoint and starts fresh with the given config.
func (m *CheckpointManager) Reset(config *Config) {
	m.checkpoint = newCheckpoint(config)
}

func (m *CheckpointManager) StartPeriodicSave(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := m.Save(m.checkpoint); err != nil {
					logger.Errorf("Failed to save checkpoint: %v", err)
				} else {
					logger.Debugf("Checkpoint saved at %s", time.Now().Format(time.RFC3339))
				}
			case <-m.stopChan:
				return
			}
		}
	}()
}

func (m *CheckpointManager) SaveOnSignal() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Infof("Received signal, saving checkpoint...")
		close(m.stopChan)

		if err := m.Save(m.checkpoint); err != nil {
			logger.Errorf("Failed to save checkpoint on signal: %v", err)
		} else {
			logger.Infof("Checkpoint saved successfully")
		}

		// TODO: use context cancel.
		os.Exit(0)
	}()
}

func trackCheckpointCompletion(key string, failed bool, mgr *CheckpointManager, config *Config) {
	if !config.EnableCheckpoint {
		return
	}
	if mgr != nil {
		if failed {
			mgr.MarkFailed(key)
		} else {
			mgr.MarkCompleted(key)
		}
		return
	}
	if config.Manager != "" {
		completionMu.Lock()
		if failed {
			failedKeysBuf = append(failedKeysBuf, key)
		} else {
			completedKeysBuf = append(completedKeysBuf, key)
		}
		completionMu.Unlock()
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
