export GO111MODULE=on
LDFLAGS = -s -w

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%cd' --date short 2>/dev/null)
PKG := github.com/juicedata/juicefs/pkg/version
LDFLAGS = -s -w
LDFLAGS += -X $(PKG).revision=$(REVISION) \
		-X $(PKG).revisionDate=$(REVISIONDATE)
GOROOT=$(shell go env GOROOT)

ifeq ($(OS),Windows_NT)
    uname_S := Windows
else
    uname_S := $(shell uname -s)
    uname_m := $(shell uname -m)
endif

ARCHNAME := amd64

ifeq ($(uname_m), aarch64)
    ARCHNAME = arm64
endif
ifeq ($(uname_m), arm64)
    ARCHNAME = arm64
endif

LIBFILE := libjfs-$(ARCHNAME).so
ifeq ($(uname_S), Windows)
    LIBFILE = libjfs-$(ARCHNAME).dll
    CC = /usr/bin/musl-gcc
    export CC
endif
ifeq ($(uname_S), Darwin)
    LIBFILE = libjfs-$(ARCHNAME).dylib
endif

all: default

default: libjfs
	mkdir -p target
	gzip -c $(LIBFILE) > target/$(LIBFILE).gz

ceph: libjfs-ceph
	mkdir -p target
	gzip -c $(LIBFILE) > target/$(LIBFILE).gz

libjfs-ceph: *.go ../../../pkg/*/*.go
	go build -tags ceph -buildmode=c-shared -ldflags="$(LDFLAGS)" -o $(LIBFILE) .

libjfs: *.go ../../../pkg/*/*.go
	go build -buildmode=c-shared -ldflags="$(LDFLAGS)" -o $(LIBFILE) .

linux-arm64: libjfs-arm64.so
	mkdir -p target
	gzip -c libjfs-arm64.so > target/libjfs-arm64.so.gz

libjfs-arm64.so: *.go ../../../pkg/*/*.go
	GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc go build -buildmode=c-shared -ldflags="$(LDFLAGS)" -o libjfs-arm64.so .

mac: libjfs.dylib
	mkdir -p target
	gzip -c libjfs-amd64.dylib > target/libjfs-amd64.dylib.gz

libjfs.dylib: *.go ../../../pkg/*/*.go
	GOOS=darwin CGO_ENABLED=1 CC=o64-clang go build -o libjfs-amd64.dylib \
	-buildmode=c-shared -ldflags="$(LDFLAGS)"

mac-arm64: libjfs-arm64.dylib
	mkdir -p target
	gzip -c libjfs-arm64.dylib > target/libjfs-arm64.dylib.gz

libjfs-arm64.dylib: *.go ../../../pkg/*/*.go
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 CC=o64-clang go build -o libjfs-arm64.dylib \
	-buildmode=c-shared -ldflags="$(LDFLAGS)"

/usr/local/include/winfsp:
	mkdir -p /usr/local/include/winfsp
	cp ../../../hack/winfsp_headers/* /usr/local/include/winfsp

win: libjfs.dll
	mkdir -p target
	gzip -c libjfs-amd64.dll > target/libjfs-amd64.dll.gz

libjfs.dll: /usr/local/include/winfsp *.go ../../../pkg/*/*.go
	GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -o libjfs-amd64.dll \
	-buildmode=c-shared -ldflags="$(LDFLAGS)"
