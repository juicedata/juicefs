DURATION ?= 10

all: xattrs fsx s3test


xattrs:
	touch /tmp/jfs-unit-test/test_xattrs
	setfattr -n user.k -v value /tmp/jfs-unit-test/test_xattrs
	getfattr -n user.k /tmp/jfs-unit-test/test_xattrs | grep -q user.k=

fsracer: secfs.test/tools/bin/fsracer
	secfs.test/tools/bin/fsracer $(DURATION) /tmp/jfs-unit-test > fsracer.log

fsx: secfs.test/tools/bin/fsx
	secfs.test/tools/bin/fsx -d $(DURATION) -p 10000 -F 100000 /tmp/jfs-unit-test/fsx.out

secfs.test/tools/bin/fsx: secfs.test

secfs.test/tools/bin/fsracer: secfs.test

secfs.test:
	git clone https://github.com/billziss-gh/secfs.test.git
	make -C secfs.test >secfs.test-build-integration.log 2>&1

s3test:
	pip install awscli --upgrade
	bash s3gateway_test.sh

webdav.test:
	/home/travis/.m2/litmus-0.13/basic http://localhost:9009
	/home/travis/.m2/litmus-0.13/copymove http://localhost:9009
	/home/travis/.m2/litmus-0.13/http http://localhost:9009
