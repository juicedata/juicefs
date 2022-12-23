FROM juicedata/mount:nightly
COPY . .
RUN apt-get update && apt-get install -y musl-tools upx-ucl && STATIC=1 make
RUN mv -f juicefs /usr/local/bin/juicefs
RUN /usr/local/bin/juicefs version