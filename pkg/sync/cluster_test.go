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
package sync

import (
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/object"
)

type obj struct {
	key       string
	size      int64
	mtime     time.Time
	isDir     bool
	isSymlink bool
}

func (o *obj) Key() string      { return o.key }
func (o *obj) Size() int64      { return o.size }
func (o *obj) Mtime() time.Time { return o.mtime }
func (o *obj) IsDir() bool      { return o.isDir }
func (o *obj) IsSymlink() bool  { return o.isSymlink }

func TestCluster(t *testing.T) {
	// manager
	todo := make(chan object.Object, 100)
	addr, err := startManager(todo)
	if err != nil {
		t.Fatal(err)
	}
	// sendStats(addr)
	// worker
	var conf Config
	conf.Manager = addr
	mytodo := make(chan object.Object, 100)
	go fetchJobs(mytodo, &conf)

	todo <- &obj{key: "test"}
	close(todo)

	obj := <-mytodo
	if obj.Key() != "test" {
		t.Fatalf("expect test but got %s", obj.Key())
	}
	if _, ok := <-mytodo; ok {
		t.Fatalf("should end")
	}
}
