export GO111MODULE=on

all: juicefs

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%ad' --date short 2>/dev/null)
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

juicefs: Makefile cmd/*.go pkg/*/*.go
	go build -ldflags="$(LDFLAGS)"  -o juicefs ./cmd

juicefs.lite: Makefile cmd/*.go pkg/*/*.go
	go build -tags nogateway,nocos,nobos,nohdfs,noibmcos,nobs,nooss,noqingstor,noscs,nosftp,noswift,noupyun,noazure,nogs \
		-ldflags="$(LDFLAGS)" -o juicefs.lite ./cmd

juicefs.ceph: Makefile cmd/*.go pkg/*/*.go
	go build -tags ceph -ldflags="$(LDFLAGS)"  -o juicefs.ceph ./cmd

/usr/local/include/winfsp:
	sudo mkdir -p /usr/local/include/winfsp
	sudo cp hack/winfsp_headers/* /usr/local/include/winfsp

juicefs.exe: /usr/local/include/winfsp cmd/*.go pkg/*/*.go
	GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
	     go build -ldflags="$(LDFLAGS)" -buildmode exe -o juicefs.exe ./cmd

.PHONY: snapshot release test
snapshot:
	docker run --rm --privileged \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		juicedata/golang-cross:latest release --snapshot --rm-dist --skip-publish

release:
	docker run --rm --privileged \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		--env-file .release-env \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		juicedata/golang-cross:latest release --rm-dist

test:
	go test -v -cover ./pkg/... ./cmd/...
