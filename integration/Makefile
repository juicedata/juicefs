DURATION ?= 10

all: xattrs fsx s3test webdav sudocache sudonocache nosudocache nosudonocache


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

webdav:
	cd /home/travis/.m2/litmus-0.13 ; for i in "basic" "copymove" "http"; do sudo ./$${i} http://127.0.0.1:9009 ; done

sudocache:
	bash ioctl_test.sh /tmp/sudocache/sudocache1
	bash ioctl_test.sh /tmp/sudocache/sudocache2
	bash ioctl_test.sh /tmp/sudocache/sudocache3


sudonocache:
	bash ioctl_test.sh /tmp/sudonocache/sudonocache1
	bash ioctl_test.sh /tmp/sudonocache/sudonocache2
	bash ioctl_test.sh /tmp/sudonocache/sudonocache3


nosudocache:
	bash ioctl_test.sh /tmp/nosudocache/nosudocache1
	bash ioctl_test.sh /tmp/nosudocache/nosudocache2
	bash ioctl_test.sh /tmp/nosudocache/nosudocache3


nosudonocache:
	bash ioctl_test.sh /tmp/nosudonocache/nosudonocache1
	bash ioctl_test.sh /tmp/nosudonocache/nosudonocache2
	bash ioctl_test.sh /tmp/nosudonocache/nosudonocache3
