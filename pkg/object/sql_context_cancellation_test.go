//go:build !nosqlite
// +build !nosqlite

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

package object

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLGet_ContextCanceled(t *testing.T) {
	store, err := newSQLStore("sqlite3", filepath.Join(t.TempDir(), "teststore.db"), "", "")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = store.Get(ctx, "object", 0, -1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestSQLRequests_ContextCancellation(t *testing.T) {
	contexts := []struct {
		name string
		new  func() (context.Context, context.CancelFunc)
		want error
	}{
		{"canceled", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, context.Canceled},
		{"deadline", func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, context.DeadlineExceeded},
	}
	operations := []struct {
		name string
		run  func(ObjectStorage, context.Context) error
	}{
		{"Get", func(s ObjectStorage, ctx context.Context) error {
			r, err := s.Get(ctx, "object", 0, -1)
			if r != nil {
				r.Close()
			}
			return err
		}},
		{"Put", func(s ObjectStorage, ctx context.Context) error {
			return s.Put(ctx, "new", bytes.NewBufferString("new data"))
		}},
		{"Overwrite", func(s ObjectStorage, ctx context.Context) error {
			return s.Put(ctx, "object", bytes.NewBufferString("replacement"))
		}},
		{"Head", func(s ObjectStorage, ctx context.Context) error {
			_, err := s.Head(ctx, "object")
			return err
		}},
		{"Delete", func(s ObjectStorage, ctx context.Context) error {
			return s.Delete(ctx, "object")
		}},
		{"List", func(s ObjectStorage, ctx context.Context) error {
			_, _, _, err := s.List(ctx, "", "", "", "", 10, true)
			return err
		}},
	}
	for _, ct := range contexts {
		t.Run(ct.name, func(t *testing.T) {
			for _, op := range operations {
				t.Run(op.name, func(t *testing.T) {
					s, err := newSQLStore("sqlite3", filepath.Join(t.TempDir(), "store.db"), "", "")
					if err != nil {
						t.Fatal(err)
					}
					defer s.(*sqlStore).db.Close()
					if err := s.Put(context.Background(), "object", bytes.NewBufferString("original")); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := ct.new()
					defer cancel()
					if err := op.run(s, ctx); !errors.Is(err, ct.want) {
						t.Errorf("expected %v, got %v", ct.want, err)
					}
					r, err := s.Get(context.Background(), "object", 0, -1)
					if err != nil {
						t.Fatal(err)
					}
					defer r.Close()
					data, err := io.ReadAll(r)
					if err != nil || string(data) != "original" {
						t.Errorf("canceled operation changed existing object: %q, %v", data, err)
					}
					if _, err := s.Head(context.Background(), "new"); err == nil {
						t.Error("canceled Put created an object")
					}
				})
			}
		})
	}
}
