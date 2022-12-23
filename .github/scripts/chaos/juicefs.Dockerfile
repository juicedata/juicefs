FROM juicedata/mount:nightly
COPY ./juicefs /usr/local/bin/juicefs
# RUN apt-get update && apt-get install -y musl-tools upx-ucl && STATIC=1 make
# RUN cp -f juicefs /usr/local/bin/juicefs
RUN /usr/local/bin/juicefs version