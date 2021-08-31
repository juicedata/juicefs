export GO111MODULE=on
LDFLAGS = -s -w

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%ad' --date short 2>/dev/null)
PKG := github.com/juicedata/juicefs/pkg/version
LDFLAGS = -s -w
LDFLAGS += -X $(PKG).revision=$(REVISION) \
		-X $(PKG).revisionDate=$(REVISIONDATE)

all: target/libjfs.so.gz

win: libjfs.dll
	mkdir -p target
	gzip -c libjfs.dll > target/libjfs.so.gz

target/libjfs.so.gz: libjfs.so
	mkdir -p target
	gzip -c libjfs.so > target/libjfs.so.gz

libjfs.so: *.go ../../../pkg/*/*.go
	go build -buildmode=c-shared -ldflags="$(LDFLAGS)" -o libjfs.so .

libjfs.dll: *.go ../../../pkg/*/*.go
	GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -o libjfs.dll \
	-buildmode=c-shared -ldflags="$(LDFLAGS)"
