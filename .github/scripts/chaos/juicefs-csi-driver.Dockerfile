FROM juicedata/juicefs-juicedata/juicefs-csi-driver:latest

WORKDIR /app

COPY /app/juicefs /usr/local/bin/juicefs

RUN /usr/local/bin/juicefs --version
