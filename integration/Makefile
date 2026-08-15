
all: s3test webdav ioctl

s3test:
	@awscli_venv=$$(mktemp -d "$${TMPDIR:-/tmp}/juicefs-awscli.XXXXXX"); \
	trap 'rm -rf "$$awscli_venv"' EXIT INT TERM; \
	python3 -m venv "$$awscli_venv"; \
	"$$awscli_venv/bin/pip" install awscli==1.44.73; \
	PATH="$$awscli_venv/bin:$$PATH" bash s3gateway_test.sh

webdav:
	cd /home/travis/.m2/litmus-0.13 ; for i in "basic" "copymove" "http"; do sudo ./$${i} http://127.0.0.1:9009 root 1234; done

ioctl:
	bash ioctl_test.sh $(MOUNT_POINT)/ioctl_test 2>/dev/null
