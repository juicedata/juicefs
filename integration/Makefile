DURATION ?= 10

all: xattrs fsx s3test

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

s3test:
	git clone https://github.com/ceph/s3-tests.git
	sudo apt-get update
	sudo apt-get install python3-virtualenv
	cd s3-tests && ./bootstrap && cp s3tests.conf.SAMPLE config.yml && sed -i '0,/^access_key.*/s/^access_key.*/access_key = testUser/' config.yml && sed -i '0,/^secret_key.*/s/^secret_key.*/secret_key = testUserPassword/' config.yml && S3TEST_CONF=./config.yml ./virtualenv/bin/nosetests -v --collect-only
