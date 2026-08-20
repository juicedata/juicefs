FROM ubuntu:22.04
ADD https://raw.githubusercontent.com/vegardit/fast-apt-mirror.sh/v1/fast-apt-mirror.sh /usr/local/bin/fast-apt-mirror.sh
RUN chmod 755 /usr/local/bin/fast-apt-mirror.sh && \
    /usr/local/bin/fast-apt-mirror.sh find --apply --ignore-sync-state || true
RUN apt update && apt install --no-install-recommends openssh-server sudo -y
RUN groupadd juicedata && useradd -ms /bin/bash -g juicedata juicedata -u 1024
RUN mkdir /var/jfs
RUN mkdir -p /home/juicedata/.ssh
COPY id_rsa.pub /home/juicedata/.ssh/authorized_keys
RUN chown juicedata:juicedata /home/juicedata/.ssh/authorized_keys && chmod 600 /home/juicedata/.ssh/authorized_keys
RUN service ssh start
EXPOSE 22
CMD ["/usr/sbin/sshd","-D"]
