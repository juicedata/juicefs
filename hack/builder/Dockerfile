FROM goreng/golang-cross:v1.15.7

ARG APT_MIRROR
RUN sed -i -e "s/deb.debian.org/${APT_MIRROR:-deb.debian.org}/g" \
           -e "s/security.debian.org/${APT_MIRROR:-security.debian.org}/g" \
           /etc/apt/sources.list && \
    apt-get update && apt-get install -y musl-tools && apt -y autoremove && \
    apt-get clean && rm -rf /var/cache/apt/* /var/lib/apt/lists/* /tmp/* /var/tmp/*
