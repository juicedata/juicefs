FROM juicedata/juicefs-csi-driver:latest

WORKDIR /home/runner/work/juicefs/juicefs/app

COPY juicefs /usr/local/bin/juicefs

# RUN /usr/local/bin/juicefs --version
