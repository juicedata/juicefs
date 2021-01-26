export GO111MODULE=on
LDFLAGS = -s -w

all: target/libjfs.so.gz

target/libjfs.so.gz: libjfs.so
	mkdir -p target
	gzip -c libjfs.so > target/libjfs.so.gz

libjfs.so: *.go ../../../pkg/*/*.go
	go build -buildmode=c-shared -ldflags="$(LDFLAGS)" -o libjfs.so .
