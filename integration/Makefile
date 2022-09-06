DURATION ?= 10

all: ioctl xattrs fsx s3test webdav


xattrs:
	touch /jfs/test_xattrs
	setfattr -n user.k -v value /jfs/test_xattrs
	getfattr -n user.k /jfs/test_xattrs | grep -q user.k=

fsracer: secfs.test/tools/bin/fsracer
	secfs.test/tools/bin/fsracer $(DURATION) /tmp/jfs-unit-test > fsracer.log

fsx: secfs.test/tools/bin/fsx
	secfs.test/tools/bin/fsx -d $(DURATION) -p 10000 -F 100000 /jfs/fsx.out

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

ioctl:
	echo ----- ioctl_test1 start-----; bash -x ioctl_test.sh /jfs/ioctl_test1;  echo ----- ioctl_test1 -- ;
	echo ----- ioctl_test2 start-----; bash -x ioctl_test.sh /jfs/ioctl_test2;  echo ----- ioctl_test2 -- ;
	echo ----- ioctl_test3 start-----; bash -x ioctl_test.sh /jfs/ioctl_test3;  echo ----- ioctl_test3 -- ;
	echo ----- ioctl_test4 start-----; bash -x ioctl_test.sh /jfs/ioctl_test4;  echo ----- ioctl_test4 -- ;
	echo ----- ioctl_test5 start-----; bash -x ioctl_test.sh /jfs/ioctl_test5;  echo ----- ioctl_test5 -- ;
	echo ----- ioctl_test6 start-----; bash -x ioctl_test.sh /jfs/ioctl_test6;  echo ----- ioctl_test6 -- ;
	echo ----- ioctl_test7 start-----; bash -x ioctl_test.sh /jfs/ioctl_test7;  echo ----- ioctl_test7 -- ;
	echo ----- ioctl_test8 start-----; bash -x ioctl_test.sh /jfs/ioctl_test8;  echo ----- ioctl_test8 -- ;
	echo ----- ioctl_test9 start-----; bash -x ioctl_test.sh /jfs/ioctl_test9;  echo ----- ioctl_test9 -- ;
	echo ----- ioctl_test10 start-----; bash -x ioctl_test.sh /jfs/ioctl_test10; echo ----ioctl_test10----- ;
	echo ----- ioctl_test11 start-----; bash -x ioctl_test.sh /jfs/ioctl_test11; echo ----ioctl_test11----- ;
	echo ----- ioctl_test12 start-----; bash -x ioctl_test.sh /jfs/ioctl_test12; echo ----ioctl_test12----- ;
	echo ----- ioctl_test13 start-----; bash -x ioctl_test.sh /jfs/ioctl_test13; echo ----ioctl_test13----- ;
	echo ----- ioctl_test14 start-----; bash -x ioctl_test.sh /jfs/ioctl_test14; echo ----ioctl_test14----- ;
	echo ----- ioctl_test15 start-----; bash -x ioctl_test.sh /jfs/ioctl_test15; echo ----ioctl_test15----- ;
	echo ----- ioctl_test16 start-----; bash -x ioctl_test.sh /jfs/ioctl_test16; echo ----ioctl_test16----- ;
	echo ----- ioctl_test17 start-----; bash -x ioctl_test.sh /jfs/ioctl_test17; echo ----ioctl_test17----- ;
	echo ----- ioctl_test18 start-----; bash -x ioctl_test.sh /jfs/ioctl_test18; echo ----ioctl_test18----- ;
	echo ----- ioctl_test19 start-----; bash -x ioctl_test.sh /jfs/ioctl_test19; echo ----ioctl_test19----- ;
	echo ----- ioctl_test20 start-----; bash -x ioctl_test.sh /jfs/ioctl_test20; echo ----ioctl_test20----- ;
	echo ----- ioctl_test21 start-----; bash -x ioctl_test.sh /jfs/ioctl_test21; echo ----ioctl_test21----- ;
	echo ----- ioctl_test22 start-----; bash -x ioctl_test.sh /jfs/ioctl_test22; echo ----ioctl_test22----- ;
	echo ----- ioctl_test23 start-----; bash -x ioctl_test.sh /jfs/ioctl_test23; echo ----ioctl_test23----- ;
	echo ----- ioctl_test24 start-----; bash -x ioctl_test.sh /jfs/ioctl_test24; echo ----ioctl_test24----- ;
	echo ----- ioctl_test25 start-----; bash -x ioctl_test.sh /jfs/ioctl_test25; echo ----ioctl_test25----- ;
	echo ----- ioctl_test26 start-----; bash -x ioctl_test.sh /jfs/ioctl_test26; echo ----ioctl_test26----- ;
	echo ----- ioctl_test27 start-----; bash -x ioctl_test.sh /jfs/ioctl_test27; echo ----ioctl_test27----- ;
	echo ----- ioctl_test28 start-----; bash -x ioctl_test.sh /jfs/ioctl_test28; echo ----ioctl_test28----- ;
	echo ----- ioctl_test29 start-----; bash -x ioctl_test.sh /jfs/ioctl_test29; echo ----ioctl_test29----- ;
	echo ----- ioctl_test30 start-----; bash -x ioctl_test.sh /jfs/ioctl_test30; echo ----ioctl_test30----- ;




