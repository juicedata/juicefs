export GO111MODULE=on
LDFLAGS = -s -w

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%ad' --date short 2>/dev/null)
VERSION := $(shell git describe --tags --match 'v*' 2>/dev/null | sed -e 's/^v//' -e 's/-g[0-9a-f]\{7,\}$$//')
PKG := github.com/juicedata/juicefs/pkg/version
LDFLAGS = -s -w
ifneq ($(strip $(VERSION)),)
	LDFLAGS += -X $(PKG).revision=$(REVISION) \
		   -X $(PKG).revisionDate=$(REVISIONDATE) \
		   -X $(PKG).version=$(VERSION)
endif

all: target/libjfs.so.gz

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%ad' --date short 2>/dev/null)
VERSION := $(shell git describe --tags --match 'v*' 2>/dev/null | sed -e 's/^v//' -e 's/-g[0-9a-f]\{7,\}$$//')
PKG := github.com/juicedata/juicefs/pkg/version
LDFLAGS = -s -w
ifneq ($(strip $(VERSION)),)
	LDFLAGS += -X $(PKG).revision=$(REVISION) \
		   -X $(PKG).revisionDate=$(REVISIONDATE) \
		   -X $(PKG).version=$(VERSION)
else ifneq ($(strip $(REVISION)),) # Use git clone with --depth or --no-tags
	LDFLAGS += -X $(PKG).revision=$(REVISION) \
		   -X $(PKG).revisionDate=$(REVISIONDATE)
endif

target/libjfs.so.gz: libjfs.so
	mkdir -p target
	gzip -c libjfs.so > target/libjfs.so.gz

libjfs.so: *.go ../../../pkg/*/*.go
	go build -buildmode=c-shared -ldflags="$(LDFLAGS)" -o libjfs.so .
