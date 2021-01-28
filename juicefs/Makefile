export GO111MODULE=on

all: juicefs

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%ad' --date short 2>/dev/null)
VERSION := $(shell git describe --tags --match 'v*' 2>/dev/null | sed -e 's/^v//' -e 's/-g[0-9a-f]\{7,\}$$//')

LDFLAGS = -s -w
ifneq ($(strip $(VERSION)),)
	LDFLAGS += -X main.revision=$(REVISION) \
		   -X main.revisionDate=$(REVISIONDATE) \
		   -X main.version=$(VERSION)
endif

SHELL = /bin/sh

ifdef STATIC
	LDFLAGS += -linkmode external -extldflags '-static'
	CC = /usr/bin/musl-gcc
	export CC
endif

juicefs: Makefile cmd/*.go pkg/*/*.go
	go build -ldflags="$(LDFLAGS)"  -o juicefs ./cmd

juicefs.ceph: Makefile cmd/*.go pkg/*/*.go
	go build -tags ceph -ldflags="$(LDFLAGS)"  -o juicefs.ceph ./cmd

.PHONY: snapshot release test
snapshot:
	docker run --rm --privileged \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		goreng/golang-cross:latest release --snapshot --rm-dist --skip-publish

release:
	docker run --rm --privileged \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		--env-file .release-env \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		goreng/golang-cross:latest release --rm-dist

test:
	go test ./pkg/... ./cmd/...
