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
	"context"

	"github.com/juicedata/juicefs/pkg/object"
)

func drain(ch <-chan object.Object) {
	go func() {
		for range ch {
		}
	}()
}

func inListRange(key, start, end string, includeStart bool) bool {
	if start != "" && (key < start || !includeStart && key == start) {
		return false
	}
	return end == "" || key <= end
}

// listAllWithExternalSort is listAll for storages whose listing order can't be trusted.
func listAllWithExternalSort(ctx context.Context, store object.ObjectStorage, prefix, start, end string, followLink, includeStart bool, sortDir string) (<-chan object.Object, error) {
	logger.Debugf("Iterating objects from %s with prefix %s start %q, sorted under %s", store, prefix, start, sortDir)
	raw, err := object.ListAll(object.WithUnorderedList(ctx), store, prefix, "", followLink, false)
	if err != nil {
		return nil, err
	}
	in := make(chan object.Object, 1000)
	go func() {
		defer close(in)
		defer drain(raw)
		for o := range raw {
			if o != nil && !inListRange(o.Key(), start, end, includeStart) {
				continue
			}
			select {
			case in <- o:
			case <-ctx.Done():
				return
			}
			if o == nil {
				return
			}
		}
	}()
	return object.SortObjects(ctx, sortDir, in), nil
}

// listCommonPrefixWithExternalSort is listCommonPrefix for storages whose listing order can't be trusted.
func listCommonPrefixWithExternalSort(ctx context.Context, store object.ObjectStorage, prefix string, cp chan object.Object, followLink bool, startAfter string, onChildPrefix func(string), sortDir string) (<-chan object.Object, error) {
	ctx = object.WithUnorderedList(ctx)
	sortCtx, cancel := context.WithCancel(ctx)
	in := make(chan object.Object, 1000)
	sorted := object.SortObjects(sortCtx, sortDir, in)
	var dirs []object.Object
	var marker, token string
	limit := delimiterListLimit(store)
	for {
		objs, hasMore, nextToken, err := store.List(ctx, prefix, marker, token, "/", limit, followLink)
		if err != nil {
			cancel()
			close(in)
			return nil, err
		}
		for _, o := range objs {
			if startAfter != "" && o.Key() <= startAfter {
				continue
			}
			if o.IsDir() && o.Key() > prefix {
				dirs = append(dirs, o)
			} else {
				in <- o
			}
		}
		if len(objs) > 0 {
			marker = objs[len(objs)-1].Key()
		}
		token = nextToken
		if !hasMore {
			break
		}
	}
	close(in)

	out := make(chan object.Object, 1000)
	go func() {
		defer close(out)
		defer cancel()
		if cp != nil {
			for _, d := range dirs {
				if onChildPrefix != nil {
					onChildPrefix(d.Key())
				}
				cp <- d
			}
		}
		for o := range sorted {
			select {
			case out <- o:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
