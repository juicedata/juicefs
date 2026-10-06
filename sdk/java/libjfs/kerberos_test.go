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

package main

import (
	"encoding/json"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

type cleanupTokenMeta struct {
	meta.Meta
	tokens      map[uint32][]byte
	listErr     syscall.Errno
	deleteErr   syscall.Errno
	listCalls   int
	deleteCalls int
}

func (m *cleanupTokenMeta) ListTokens(meta.Context) (map[uint32][]byte, syscall.Errno) {
	m.listCalls++
	return m.tokens, m.listErr
}

func (m *cleanupTokenMeta) DeleteTokens(_ meta.Context, ids []uint32) syscall.Errno {
	m.deleteCalls++
	if m.deleteErr != 0 {
		return m.deleteErr
	}
	for _, id := range ids {
		delete(m.tokens, id)
	}
	return 0
}

func cleanupTokenData(t *testing.T, expire int64) []byte {
	t.Helper()
	data, err := json.Marshal(&token{Expire: expire})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCleanupTokensVisitsEveryVolume(t *testing.T) {
	for _, tc := range []struct {
		name    string
		listErr syscall.Errno
		active  bool
	}{
		{name: "empty"},
		{name: "unexpired", active: true},
		{name: "list_error", listErr: syscall.EIO},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &kerberos{vols: make(map[string]*volParams)}
			metas := make([]*cleanupTokenMeta, 3)
			for i := range metas {
				m := &cleanupTokenMeta{listErr: tc.listErr, tokens: make(map[uint32][]byte)}
				if tc.active {
					m.tokens[1] = cleanupTokenData(t, time.Now().Add(time.Hour).Unix())
				}
				metas[i] = m
				k.vols[fmt.Sprintf("vol%d", i)] = &volParams{m: m}
			}
			k.cleanupTokens()
			// Every volume must be visited, regardless of map iteration order.
			for i, m := range metas {
				if m.listCalls != 1 || m.deleteCalls != 0 {
					t.Errorf("volume %d: list calls=%d, delete calls=%d", i, m.listCalls, m.deleteCalls)
				}
			}
		})
	}
}

func TestCleanupTokensDeletesOnlyExpired(t *testing.T) {
	k := &kerberos{vols: make(map[string]*volParams)}
	metas := make([]*cleanupTokenMeta, 2)
	for i := range metas {
		m := &cleanupTokenMeta{tokens: map[uint32][]byte{
			1: cleanupTokenData(t, time.Now().Add(-time.Hour).Unix()),
			2: cleanupTokenData(t, time.Now().Add(time.Hour).Unix()),
		}}
		metas[i] = m
		k.vols[fmt.Sprintf("vol%d", i)] = &volParams{m: m}
	}
	k.cleanupTokens()
	for i, m := range metas {
		if m.listCalls != 1 || m.deleteCalls != 1 || len(m.tokens) != 1 || m.tokens[2] == nil {
			t.Errorf("volume %d: list calls=%d, delete calls=%d, remaining=%v", i, m.listCalls, m.deleteCalls, m.tokens)
		}
	}
}

func TestCleanupTokensDeleteFailureVisitsOtherVolumes(t *testing.T) {
	k := &kerberos{vols: make(map[string]*volParams)}
	metas := make([]*cleanupTokenMeta, 2)
	for i := range metas {
		m := &cleanupTokenMeta{deleteErr: syscall.EIO, tokens: map[uint32][]byte{
			1: cleanupTokenData(t, time.Now().Add(-time.Hour).Unix()),
		}}
		metas[i] = m
		k.vols[fmt.Sprintf("vol%d", i)] = &volParams{m: m}
	}
	k.cleanupTokens()
	for i, m := range metas {
		if m.listCalls != 1 || m.deleteCalls != 1 || len(m.tokens) != 1 {
			t.Errorf("volume %d: list calls=%d, delete calls=%d, remaining=%v", i, m.listCalls, m.deleteCalls, m.tokens)
		}
	}
}
