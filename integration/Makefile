
AWSCLI_VENV=/tmp/juicefs-awscli-venv
AWSCLI=$(AWSCLI_VENV)/bin/aws
ZEROFS_DIR?=
ZEROFS_BIN?=
JUICEFS_BIN?=

all: s3test webdav ioctl

$(AWSCLI):
	python3 -m venv $(AWSCLI_VENV)
	$(AWSCLI_VENV)/bin/pip install --upgrade pip setuptools wheel
	$(AWSCLI_VENV)/bin/pip install awscli==1.44.73

s3test: $(AWSCLI)
	PATH="$(AWSCLI_VENV)/bin:$$PATH" bash s3gateway_test.sh

zerofs-smoke: $(AWSCLI)
	PATH="$(AWSCLI_VENV)/bin:$$PATH" AWS_BIN="$(AWSCLI)" ZEROFS_DIR="$(ZEROFS_DIR)" ZEROFS_BIN="$(ZEROFS_BIN)" JUICEFS_BIN="$(JUICEFS_BIN)" bash zerofs_smoke_test.sh

webdav:
	cd /home/travis/.m2/litmus-0.13 ; for i in "basic" "copymove" "http"; do sudo ./$${i} http://127.0.0.1:9009 root 1234; done

ioctl:
	bash ioctl_test.sh /tmp/jfs-unit-test/ioctl_test 2>/dev/null
