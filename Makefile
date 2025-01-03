export GO111MODULE=on

all: juicefs

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%cd' --date short 2>/dev/null)
PKG := github.com/juicedata/juicefs/pkg/version
LDFLAGS = -s -w
ifneq ($(strip $(REVISION)),) # Use git clone
	LDFLAGS += -X $(PKG).revision=$(REVISION) \
		   -X $(PKG).revisionDate=$(REVISIONDATE)
endif

SHELL = /bin/sh

ifdef STATIC
	LDFLAGS += -linkmode external -extldflags '-static'
	CC = /usr/bin/musl-gcc
	export CC
endif

juicefs: Makefile cmd/*.go pkg/*/*.go go.*
	go version
	go build -ldflags="$(LDFLAGS)"  -o juicefs .

juicefs.cover: Makefile cmd/*.go pkg/*/*.go go.*
	go version
	go build -ldflags="$(LDFLAGS)"  -cover -o juicefs .

juicefs.lite: Makefile cmd/*.go pkg/*/*.go
	go build -tags nogateway,nowebdav,nocos,nobos,nohdfs,noibmcos,noobs,nooss,noqingstor,noscs,nosftp,noswift,noupyun,noazure,nogs,noufile,nob2,nonfs,nodragonfly,nosqlite,nomysql,nopg,notikv,nobadger,noetcd \
		-ldflags="$(LDFLAGS)" -o juicefs.lite .

juicefs.ceph: Makefile cmd/*.go pkg/*/*.go
	go build -tags ceph -ldflags="$(LDFLAGS)"  -o juicefs.ceph .

juicefs.fdb: Makefile cmd/*.go pkg/*/*.go
	go build -tags fdb -ldflags="$(LDFLAGS)"  -o juicefs.fdb .

juicefs.fdb.cover: Makefile cmd/*.go pkg/*/*.go
	go build -tags fdb -ldflags="$(LDFLAGS)" -cover -o juicefs.fdb .

juicefs.gluster: Makefile cmd/*.go pkg/*/*.go
	go build -tags gluster -ldflags="$(LDFLAGS)"  -o juicefs.gluster .

juicefs.gluster.cover: Makefile cmd/*.go pkg/*/*.go
	go build -tags gluster -ldflags="$(LDFLAGS)"  -cover -o juicefs.gluster .

juicefs.all: Makefile cmd/*.go pkg/*/*.go
	go build -tags ceph,fdb,gluster -ldflags="$(LDFLAGS)"  -o juicefs.all .

# This is the script for compiling the Linux version on the MacOS platform.
# Please execute the `brew install FiloSottile/musl-cross/musl-cross` command before using it.
juicefs.linux:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC=x86_64-linux-musl-gcc CGO_LDFLAGS="-static" go build -ldflags="$(LDFLAGS)"  -o juicefs .

/usr/local/include/winfsp:
	sudo mkdir -p /usr/local/include/winfsp
	sudo cp hack/winfsp_headers/* /usr/local/include/winfsp

# This is the script for compiling the Windows version on the MacOS platform.
# Please execute the `brew install mingw-w64` command before using it.
juicefs.exe: /usr/local/include/winfsp cmd/*.go pkg/*/*.go
	GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
	     go build -ldflags="$(LDFLAGS)" -buildmode exe -o juicefs.exe .

.PHONY: snapshot release test
snapshot:
	docker run --rm --privileged \
		-e REVISIONDATE=$(REVISIONDATE) \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		juicedata/golang-cross:latest release --snapshot --rm-dist --skip-publish

release:
	docker run --rm --privileged \
		-e REVISIONDATE=$(REVISIONDATE) \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		--env-file .release-env \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		juicedata/golang-cross:latest release --rm-dist

test.meta.core:
	SKIP_NON_CORE=true go test -v -cover -count=1  -failfast -timeout=12m ./pkg/meta/... -args -test.gocoverdir="$(shell realpath cover/)"

test.meta.non-core:
	go test -v -cover -run='TestRedisCluster|TestPostgreSQLClient|TestLoadDumpSlow|TestEtcdClient|TestKeyDB' -count=1  -failfast -timeout=12m ./pkg/meta/... -args -test.gocoverdir="$(shell realpath cover/)"

test.pkg:
	go test -tags gluster -v -cover -count=1  -failfast -timeout=12m $$(go list ./pkg/... | grep -v /meta) -args -test.gocoverdir="$(shell realpath cover/)"

test.cmd:
	sudo JFS_GC_SKIPPEDTIME=1 MINIO_ACCESS_KEY=testUser MINIO_SECRET_KEY=testUserPassword GOMAXPROCS=8 go test -v -count=1 -failfast -cover -timeout=8m ./cmd/... -coverpkg=./pkg/...,./cmd/... -args -test.gocoverdir="$(shell realpath cover/)"

test.fdb:
	go test -v -cover -count=1  -failfast -timeout=4m ./pkg/meta/ -tags fdb -run=TestFdb -args -test.gocoverdir="$(shell realpath cover/)"
