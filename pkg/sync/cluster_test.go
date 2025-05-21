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
	"os"
	"os/user"
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

func (o *obj) Key() string          { return o.key }
func (o *obj) Size() int64          { return o.size }
func (o *obj) Mtime() time.Time     { return o.mtime }
func (o *obj) IsDir() bool          { return o.isDir }
func (o *obj) IsSymlink() bool      { return o.isSymlink }
func (o *obj) StorageClass() string { return "" }

type file struct {
	obj
}

func (o *file) Owner() string     { return "" }
func (o *file) Group() string     { return "" }
func (o *file) Mode() os.FileMode { return 0 }

func TestCluster(t *testing.T) {
	// manager
	workerAddr := "127.0.0.1"
	if u, err := user.Current(); err != nil {
		logger.Warnf("Failed to get current user: %v", err)
	} else if u.Username != "" {
		workerAddr = u.Username + "@" + workerAddr
	}
	todo := make(chan object.Object, 100)
	var conf Config
	conf.Workers = []string{workerAddr}
	addr, err := startManager(&conf, todo)
	if err != nil {
		t.Fatal(err)
	}
	// sendStats(addr)
	// worker
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

func TestMarshal(t *testing.T) {
	mtime := time.Now()
	var objs = []object.Object{
		&obj{key: "test", mtime: mtime},
		withSize(&obj{key: "test1", size: 100}, -4),
		withSize(&file{obj{key: "test2", size: 200}}, -1),
		withSize(&file{obj{key: "test3", size: 200, isSymlink: true}}, -1),
	}
	d, err := marshalObjects(objs)
	if err != nil {
		t.Fatal(err)
	}
	objs2, e := unmarshalObjects(d)
	if e != nil {
		t.Fatal(e)
	}
	if objs2[0].Key() != "test" {
		t.Fatalf("expect test but got %s", objs2[0].Key())
	}
	if !objs2[0].Mtime().Equal(objs[0].Mtime()) {
		t.Fatalf("expect %s but got %s", mtime, objs2[0].Mtime())
	}
	if objs2[1].Key() != "test1" || objs2[1].Size() != -4 || withoutSize(objs2[1]).Size() != 100 {
		t.Fatalf("expect withSize but got %s", objs2[1].Key())
	}
	if objs2[2].Key() != "test2" || objs2[2].Size() != -1 || withoutSize(objs2[2]).Size() != 200 {
		t.Fatalf("expect withFSize but got %s", objs2[2].Key())
	}
	if objs2[3].Key() != "test3" || objs2[3].Size() != -1 || withoutSize(objs2[3]).Size() != 200 && objs2[3].IsSymlink() != true {
		t.Fatalf("expect withFSize but got %s", objs2[3].Key())
	}
}
