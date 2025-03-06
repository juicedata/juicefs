
all: s3test webdav ioctl

s3test:
	pip install awscli==1.27.153
	bash s3gateway_test.sh

webdav:
	cd /home/travis/.m2/litmus-0.13 ; for i in "basic" "copymove" "http"; do sudo ./$${i} http://127.0.0.1:9009 root 1234; done

ioctl:
	bash ioctl_test.sh /tmp/jfs-unit-test/ioctl_test 2>/dev/null
