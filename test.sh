#!/bin/bash
for i in {1..100}
do
   echo "test $i: "
   go clean -testcache && go test -run '^TestSQLiteClient$' github.com/juicedata/juicefs/pkg/meta
done
