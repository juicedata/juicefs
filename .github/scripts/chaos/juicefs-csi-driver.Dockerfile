FROM juicedata/juicefs-juicedata/juicefs-csi-driver:latest

WORKDIR /tmp/app

COPY /tmp/app/juicefs /usr/local/bin/juicefs

RUN /usr/local/bin/juicefs --version
