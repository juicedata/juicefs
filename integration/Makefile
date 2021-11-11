DURATION ?= 10

all: fsx xattrs fsracer

xattrs:
	touch /tmp/jfs/test_xattrs
	setfattr -n user.k -v value /tmp/jfs/test_xattrs
	getfattr -n user.k /tmp/jfs/test_xattrs | grep -q user.k=

fsracer: secfs.test/tools/bin/fsracer
	secfs.test/tools/bin/fsracer $(DURATION) /tmp/jfs > fsracer.log

fsx: secfs.test/tools/bin/fsx
	secfs.test/tools/bin/fsx -d $(DURATION) -p 10000 -F 100000 /tmp/jfs/fsx.out

secfs.test/tools/bin/fsx: secfs.test

secfs.test/tools/bin/fsracer: secfs.test

secfs.test:
	git clone https://github.com/billziss-gh/secfs.test.git
	make -C secfs.test >secfs.test-build-integration.log 2>&1

