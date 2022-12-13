FROM golang:1.18-buster as builder

ARG GOPROXY
# refs/remotes/pull/3056/merge
ARG GITHUB_REF
# 4ac69613b5919142d87f21a64ca744ae537192d6
ARG GITHUB_SHA
ARG JUICEFS_REPO_URL=https://github.com/juicedata/juicefs

WORKDIR /workspace
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org}
ENV STATIC=1

RUN apt-get update && apt-get install -y musl-tools upx-ucl && \
    cd /workspace && git clone --depth=1 $JUICEFS_REPO_URL && \
    cd juicefs && git fetch --no-tags --prune origin +$GITHUB_SHA:$GITHUB_REF && \
    git checkout $GITHUB_REF && \
    make juicefs

FROM juicedata/juicefs-csi-driver:nightly

WORKDIR /app
COPY --from=builder /workspace/juicefs/juicefs /usr/local/bin/

RUN ls -l /usr/local/bin/juicefs

RUN /usr/local/bin/juicefs --version
RUN echo GITHUB_REF is $GITHUB_REF
RUN echo GITHUB_SHA is $GITHUB_SHA

# ENTRYPOINT ["/tini", "--", "/bin/juicefs-csi-driver"]
