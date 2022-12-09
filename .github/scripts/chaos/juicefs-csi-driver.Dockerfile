FROM golang:1.18-buster as builder

ARG GOPROXY
ARG JUICEFS_REPO_URL=https://github.com/juicedata/juicefs
ARG JUICEFS_REPO_BRANCH=main
ARG JUICEFS_REPO_REF=${JUICEFS_REPO_BRANCH}

WORKDIR /workspace
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org}
RUN apt-get update && apt-get install -y musl-tools upx-ucl && \
    cd /workspace && git clone --branch=$JUICEFS_REPO_BRANCH $JUICEFS_REPO_URL && \
    cd juicefs && git checkout $JUICEFS_REPO_REF && make juicefs


FROM juicedata/juicefs-csi-driver:latest

WORKDIR /app
COPY --from=builder /workspace/juicefs/juicefs /usr/local/bin/

RUN ls -l /usr/local/bin/juicefs

# RUN /usr/local/bin/juicefs --version

ENTRYPOINT ["/tini", "--", "/bin/juicefs-csi-driver"]
