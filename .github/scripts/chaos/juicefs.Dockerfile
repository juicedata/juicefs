# Copyright 2018 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.18-buster as builder

ARG GOPROXY
ARG JUICEFS_REPO_URL=https://github.com/juicedata/juicefs
ARG JUICEFS_REPO_BRANCH=main
ARG JUICEFS_REPO_REF=${JUICEFS_REPO_BRANCH}

WORKDIR /workspace
ENV GOPROXY=${GOPROXY:-https://proxy.golang.org}
RUN apt-get update && apt-get install -y musl-tools upx-ucl librados-dev && \
    cd /workspace && git clone --branch=$JUICEFS_REPO_BRANCH $JUICEFS_REPO_URL && \
    cd juicefs && git checkout $JUICEFS_REPO_REF && make juicefs.ceph && mv juicefs.ceph juicefs

FROM python:3.8-slim-buster

ARG JFS_AUTO_UPGRADE

WORKDIR /app
COPY --from=builder /workspace/juicefs/juicefs /usr/local/bin/

ENV JUICEFS_CLI=/usr/bin/juicefs
ENV JFS_AUTO_UPGRADE=${JFS_AUTO_UPGRADE:-enabled}
ENV JFS_MOUNT_PATH=/usr/local/juicefs/mount/jfsmount

RUN apt-get update && apt-get install -y librados2 curl fuse procps iputils-ping strace iproute2 net-tools tcpdump lsof && \
    rm -rf /var/cache/apt/* && \
    curl -sSL https://juicefs.com/static/juicefs -o ${JUICEFS_CLI} && chmod +x ${JUICEFS_CLI} && \
    mkdir -p /root/.juicefs && \
    ln -s /usr/local/bin/python /usr/bin/python && \
    mkdir /root/.acl && cp /etc/passwd /root/.acl/passwd && cp /etc/group /root/.acl/group && \
    ln -sf /root/.acl/passwd /etc/passwd && ln -sf /root/.acl/group  /etc/group

RUN ln -s /usr/local/bin/juicefs /bin/mount.juicefs
COPY THIRD-PARTY /

RUN /usr/bin/juicefs version && /usr/local/bin/juicefs --version