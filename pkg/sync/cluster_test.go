// Copyright (C) 2020-present Juicedata Inc.

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
