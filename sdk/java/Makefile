GOROOT=$(shell go env GOROOT)

all: package

libjfs/target/libjfs.so.gz: ../../pkg/*/*.go libjfs/*.go
	make -C libjfs

compile:
	mvn compile -B --quiet
test: libjfs
	mvn test -B --quiet
package: libjfs/target/libjfs.so.gz
	mvn package -B -Dmaven.test.skip=true
docker-package:
	docker run --rm --mount "type=bind,src=$(GOROOT),target=/go" \
		--mount "type=bind,src=$(HOME)/go,target=/root/go" \
		--mount "type=bind,src=$(shell pwd)/../../,target=/workspace/juicefs" \
		--workdir /workspace/juicefs juicedata/sdk-builder \
		/bin/bash -c 'cd sdk/java && make'

win: win-package package

win-package: ../../pkg/*/*.go libjfs/*.go
	make -C libjfs win
