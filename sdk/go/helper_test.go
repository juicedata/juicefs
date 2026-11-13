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

package juicefs

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/juicedata/juicefs/pkg/meta"
)

// newTestVolume formats a volume on SQLite and a local directory.
func newTestVolume(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	metaURL := "sqlite3://" + filepath.Join(dir, "meta.db")
	m := meta.NewClient(metaURL, nil)
	defer func() { _ = m.Shutdown() }()
	format := &meta.Format{
		Name:      "test",
		UUID:      uuid.New().String(),
		Storage:   "file",
		Bucket:    filepath.Join(dir, "data") + "/",
		BlockSize: 4096,
		DirStats:  true,
	}
	if err := m.Init(format, true); err != nil {
		t.Fatalf("format volume: %s", err)
	}
	return metaURL
}

// testConfig disables usage reporting, which would contact juicefs.com.
func testConfig() *Config {
	conf := DefaultConfig()
	conf.NoUsageReport = true
	return conf
}

func newTestClient(t testing.TB) *Client {
	t.Helper()
	c, err := New("test", newTestVolume(t), testConfig())
	if err != nil {
		t.Fatalf("New: %s", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// createFile creates an empty file, or truncates an existing one.
func createFile(t testing.TB, c *Client, name string) {
	t.Helper()
	if err := c.WriteFile(name, nil, 0666); err != nil {
		t.Fatalf("create %s: %s", name, err)
	}
}
