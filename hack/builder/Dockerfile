FROM ghcr.io/gythialy/golang-cross:v1.21.9-0

RUN apt-get update && apt-get install -y musl-tools && apt -y autoremove && \
    apt-get clean && rm -rf /var/cache/apt/* /var/lib/apt/lists/* /tmp/* /var/tmp/*
    git config --global --add safe.directory /go/src/github.com/juicedata/juicefs
