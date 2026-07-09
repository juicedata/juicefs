export GO111MODULE=on

all: juicefs

REVISION := $(shell git rev-parse --short HEAD 2>/dev/null)
REVISIONDATE := $(shell git log -1 --pretty=format:'%cd' --date short 2>/dev/null)
PKG := github.com/juicedata/juicefs/pkg/version
GCFLAGS =
BUILD ?= release
ifneq ($(strip $(REVISION)),) # Use git clone
	LDFLAGS += -X $(PKG).revision=$(REVISION) \
		   -X $(PKG).revisionDate=$(REVISIONDATE)
endif

ifeq ($(BUILD),release)
	LDFLAGS += -s -w
else ifeq ($(BUILD),debug)
	GCFLAGS := all=-N -l
endif

SHELL = /bin/sh

ifdef STATIC
	LDFLAGS += -linkmode external -extldflags '-static'
	CC = /usr/bin/musl-gcc
	export CC
endif

# RISC-V build knobs (override in command line when needed):
# make juicefs.riscv64 RISCV64_GORISCV64='rva23u64,zabha,zacas' \
#     RISCV64_ASM_DEFS='-D HasZvknha -D EnableSmallSizeMemVector'
RISCV64_CC ?= /usr/local/gcc
RISCV64_GORISCV64 ?=
RISCV64_ASM_DEFS ?=
RISCV64_ASMFLAGS ?= github.com/juicedata/juicefs/...='$(RISCV64_ASM_DEFS)'

juicefs: Makefile cmd/*.go pkg/*/*.go go.*
	go version
	go build -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -o juicefs .

juicefs.cover: Makefile cmd/*.go pkg/*/*.go go.*
	go version
	go build -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -cover -o juicefs .

juicefs.lite: Makefile cmd/*.go pkg/*/*.go
	go build -tags nogateway,nowebdav,nocos,nobos,nohdfs,noibmcos,noobs,nooss,noqingstor,nosftp,noswift,noazure,nogs,noufile,nob2,nonfs,nodragonfly,nosqlite,nomysql,nopg,notikv,nobadger,noetcd,nocifs,nostorj,noqiniu,notos,noks3 \
		-gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -o juicefs.lite .

juicefs.ceph: Makefile cmd/*.go pkg/*/*.go
	go build -tags ceph -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -o juicefs.ceph .

juicefs.fdb: Makefile cmd/*.go pkg/*/*.go
	go build -tags fdb -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -o juicefs.fdb .

juicefs.fdb.cover: Makefile cmd/*.go pkg/*/*.go
	go build -tags fdb -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -cover -o juicefs.fdb .

juicefs.gluster: Makefile cmd/*.go pkg/*/*.go
	go build -tags gluster -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -o juicefs.gluster .

juicefs.gluster.cover: Makefile cmd/*.go pkg/*/*.go
	go build -tags gluster -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -cover -o juicefs.gluster .

juicefs.all: Makefile cmd/*.go pkg/*/*.go
	go build -tags ceph,fdb,gluster -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -o juicefs.all .

# This is cross-compiling LoongArch in a Linux environment on x86_64 (amd64) or aarch64 (arm64) architecture.
# 1. Install LoongArch64 cross-compile toolchain from https://github.com/loong64/cross-tools
# 2. Set CC to your toolchain path.
# 3. Run `STATIC=1 make juicefs.loongarch` to build the LoongArch binary.
juicefs.loongarch: Makefile cmd/*.go pkg/*/*.go go.*
	CC=bin/loongarch64-unknown-linux-musl-cc CGO_ENABLED=1 GOARCH=loong64 go build -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -o juicefs .

# This is the script for compiling the Linux version on the MacOS platform.
# Please execute the `brew install FiloSottile/musl-cross/musl-cross` command before using it.
juicefs.linux:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC=x86_64-linux-musl-gcc CGO_LDFLAGS="-static" go build -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)"  -o juicefs .

# This is cross-compiling RISC-V in a Linux environment on x86_64 (amd64) or aarch64 (arm64) architecture.
# 1. Install RISC-V musl cross-compile toolchain at /usr/local/riscv64-linux-musl-cross
#    or override RISCV64_CC to your toolchain path.
# 2. Run `make juicefs.riscv64` to build the RISC-V binary.
# 3. Optionally set RISCV64_GORISCV64 for ISA extensions and RISCV64_ASM_DEFS for assembly defines.
#    Example:
#      make juicefs.riscv64 RISCV64_GORISCV64='rva23u64,zabha,zacas' \
#          RISCV64_ASM_DEFS='-D HasZvknha -D EnableSmallSizeMemVector'
juicefs.riscv64: Makefile cmd/*.go pkg/*/*.go go.*
	CGO_ENABLED=1 GOARCH=riscv64 GORISCV64="$(RISCV64_GORISCV64)" \
	CC="$(RISCV64_CC)" \
	go build -gcflags="$(GCFLAGS)" \
		-ldflags="$(LDFLAGS)" \
		-asmflags="$(RISCV64_ASMFLAGS)" \
		-o juicefs .

/usr/local/include/winfsp:
	sudo mkdir -p /usr/local/include/winfsp
	sudo cp hack/winfsp_headers/* /usr/local/include/winfsp

# This is the script for compiling the Windows version on the MacOS platform.
# Please execute the `brew install mingw-w64` command before using it.
juicefs.exe: /usr/local/include/winfsp cmd/*.go pkg/*/*.go
	GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
	     go build -gcflags="$(GCFLAGS)" -ldflags="$(LDFLAGS)" -buildmode exe -o juicefs.exe .

# This is the script for compiling the Windows version on Windows platform.
# Please ensure mingw64 is in PATH and WinFsp SDK is installed at C:/WinFsp
_juicefs.exe:
	powershell -Command "$$env:PATH+=';C:\mingw64\bin'; $$env:CGO_ENABLED='1'; $$env:CGO_CFLAGS='-IC:/WinFsp/inc/fuse'; go build -ldflags='-s -w' -o juicefs.exe ."

.PHONY: snapshot release debug test
snapshot:
	docker run --rm --privileged \
		-e REVISIONDATE=$(REVISIONDATE) \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		juicedata/golang-cross:v1.25.7-0 release --snapshot --clean --skip-publish

release:
	docker run --rm --privileged \
		-e REVISIONDATE=$(REVISIONDATE) \
		-e PRIVATE_KEY=${PRIVATE_KEY} \
		--env-file .release-env \
		-v ~/go/pkg/mod:/go/pkg/mod \
		-v `pwd`:/go/src/github.com/juicedata/juicefs \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w /go/src/github.com/juicedata/juicefs \
		juicedata/golang-cross:v1.25.7-0 release --clean

debug:
	$(MAKE) BUILD=debug all

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

unit-random-test:
	echo "Using meta:$(meta), seed: $(seed), checks:${checks}, steps: $(steps)"
	go test ./pkg/meta/... -rapid.meta="$(meta)" -rapid.seed=$(seed) -rapid.checks=$(checks) -rapid.steps=$(steps) -run "TestFSOps" -v -failfast -count=1 -timeout=60m -cover -coverpkg=./pkg/... -args -test.gocoverdir="$(shell realpath cover/)"
