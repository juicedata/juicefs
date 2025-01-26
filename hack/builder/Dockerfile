FROM ghcr.io/gythialy/golang-cross:v1.21.9-0

RUN apt-get update && apt-get install -y musl-tools && apt-get -y autoremove && \
    apt-get clean && rm -rf /var/cache/apt/* /var/lib/apt/lists/* /tmp/* /var/tmp/* && \
    git config --global --add safe.directory /go/src/github.com/juicedata/juicefs && \
    curl -fsSL -o /tmp/aarch64-linux-musl-cross.tgz https://musl.cc/aarch64-linux-musl-cross.tgz && \
    tar -xf /tmp/aarch64-linux-musl-cross.tgz -C /usr/local/ && rm -f /tmp/aarch64-linux-musl-cross.tgz
