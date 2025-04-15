LDFLAGS = -s -w

.PHONY: libjfs.so juicefs

# SET GOPROXY if WITH_PROXY is set
CN_GOPROXY ?= 0
ifeq ($(CN_GOPROXY), 1)
	GOPROXY = https://proxy.golang.com.cn,direct
endif

VERSION_FILE := ../../pkg/version/version.go

VERSION := $(shell awk '/major[[:space:]]*:[[:space:]]*/ {gsub(/[^0-9]/, "", $$2); major=$$2} \
    /minor[[:space:]]*:[[:space:]]*/ {gsub(/[^0-9]/, "", $$2); minor=$$2} \
    /patch[[:space:]]*:[[:space:]]*/ {gsub(/[^0-9]/, "", $$2); patch=$$2} \
    END {print major "." minor "." patch}' $(VERSION_FILE))

SHORT_HASH := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
BUILD_DATE_SHORT := $(shell date -u +'%Y%m%d%H%M')

# libjfs is located in the sdk/java/libjfs
libjfs.so:
	go build -buildmode c-shared -ldflags="$(LDFLAGS)" -o juicefs/juicefs/libjfs.so ../java/libjfs

builder: Dockerfile.builder
	docker build -t sdkbuilder -f Dockerfile.builder .

arm-builder: Dockerfile.builder.arm
	docker build -t sdkbuilder -f Dockerfile.builder.arm .

juicefs:
	sudo rm -rf juicefs.egg-info
	echo "Building juicefs version $(VERSION).$(BUILD_DATE_SHORT)"
	sed -i 's/^VERSION = .*/VERSION = "$(VERSION).$(BUILD_DATE_SHORT)"/' juicefs/setup.py
	sed -i 's/^BUILD_INFO = .*/BUILD_INFO = "$(BUILD_DATE) $(SHORT_HASH)"/' juicefs/setup.py
	docker run --rm -i -v ${PWD}/../../:/opt/jfs -w /opt/jfs/sdk/python -e GOPROXY=${GOPROXY} sdkbuilder sh -c 'make libjfs.so && cd juicefs && python3 -m build -w'

clean:
    $(clean)
