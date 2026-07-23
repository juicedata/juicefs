//go:build raceinject
// +build raceinject

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

package fuse

import (
	"os"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
)

func replyAttrRaceMaybeSleep(entry *meta.Entry) {
	if !(entry.Attr.Full && entry.Attr.Typ == meta.TypeFile && entry.Attr.Length == 0) {
		return
	}
	if sleep := os.Getenv("JUICEFS_REPLY_ATTR_RACE_SLEEP"); sleep != "" {
		if d, err := time.ParseDuration(sleep); err == nil && d > 0 {
			time.Sleep(d)
		}
	}
}
