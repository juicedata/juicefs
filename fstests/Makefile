DURATION ?= 10

all: fsracer fsx xattrs

xattrs:
	touch /jfs/test_xattrs
	setfattr -n user.k -v value /jfs/test_xattrs
	getfattr -n user.k /jfs/test_xattrs | grep -q user.k=

fsracer: healthcheck secfs.test/tools/bin/fsracer
	secfs.test/tools/bin/fsracer $(DURATION) /jfs >fsracer.log
	make healthcheck

fsx: healthcheck secfs.test/tools/bin/fsx
	secfs.test/tools/bin/fsx -d $(DURATION) -p 10000 -F 100000 /jfs/fsx.out
	make healthcheck

setup:
	redis-server &
	cd node1 && redis-server redis.conf &
	cd node2 && redis-server redis.conf &
	cd node3 && redis-server redis.conf &
	sleep 1
	echo yes | redis-cli --cluster create 127.0.0.1:7001 127.0.0.1:7002 127.0.0.1:7003
	mkdir -p /jfs
	../juicefs format localhost unittest
	../juicefs mount -d --no-usage-report --enable-xattr localhost /jfs

healthcheck:
	pgrep juicefs

secfs.test/tools/bin/fsx: secfs.test

secfs.test/tools/bin/fsracer: secfs.test

secfs.test:
	git clone https://github.com/billziss-gh/secfs.test.git
	make -C secfs.test >secfs.test-build.log 2>&1

flock:
	git clone https://github.com/gofrs/flock.git
	mkdir /jfs/tmp
	cd flock && go mod init github.com/gofrs/flock.git && go mod tidy && TMPDIR=/jfs/tmp go test .
