/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */
package sync

import (
	"testing"

	"github.com/juicedata/juicefs/pkg/object"
)

func TestCluster(t *testing.T) {
	// manager
	todo := make(chan *object.Object, 100)
	addr, err := startManager(todo)
	if err != nil {
		t.Fatal(err)
	}
	sendStats(addr)
	// worker
	var conf Config
	conf.Manager = addr
	mytodo := make(chan *object.Object, 100)
	go fetchJobs(mytodo, &conf)

	todo <- &object.Object{Key: "test"}
	close(todo)

	obj := <-mytodo
	if obj.Key != "test" {
		t.Fatalf("expect test but got %s", obj.Key)
	}
	if _, ok := <-mytodo; ok {
		t.Fatalf("should end")
	}
}
