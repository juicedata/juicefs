export GO111MODULE=on
LDFLAGS = -s -w

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%ad' --date short 2>/dev/null)
PKG := github.com/juicedata/juicefs/pkg/version
LDFLAGS = -s -w
LDFLAGS += -X $(PKG).revision=$(REVISION) \
		-X $(PKG).revisionDate=$(REVISIONDATE)
GOROOT=$(shell go env GOROOT)

LIBFILE := libjfs.so
OSNAME := linux

ifeq ($(OS),Windows_NT)
    uname_S := Windows
else
    uname_S := $(shell uname -s)
endif

ifeq ($(uname_S), Windows)
    LIBFILE = libjfs.dll
    OSNAME = windows
    CC = /usr/bin/musl-gcc
    export CC
endif
ifeq ($(uname_S), Darwin)
    LIBFILE = libjfs.dylib
    OSNAME = darwin
endif

all: default

default: libjfs
	mkdir -p target
	gzip -c $(LIBFILE) > target/$(LIBFILE).gz

libjfs: *.go ../../../pkg/*/*.go
	go build -buildmode=c-shared -ldflags="$(LDFLAGS)" -o $(LIBFILE) .

mac: libjfs.dylib
	mkdir -p target
	gzip -c libjfs.dylib > target/libjfs.dylib.gz

libjfs.dylib: *.go ../../../pkg/*/*.go
	GOOS=darwin CGO_ENABLED=1 CC=o64-clang go build -o libjfs.dylib \
	-buildmode=c-shared -ldflags="$(LDFLAGS)"

win: libjfs.dll
	mkdir -p target
	gzip -c libjfs.dll > target/libjfs.dll.gz

libjfs.dll: *.go ../../../pkg/*/*.go
	GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -o libjfs.dll \
	-buildmode=c-shared -ldflags="$(LDFLAGS)"
