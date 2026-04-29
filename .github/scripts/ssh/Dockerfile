FROM ubuntu:22.04
RUN sed -i 's|http://archive.ubuntu.com|http://mirrors.aliyun.com|g' /etc/apt/sources.list && \
    sed -i 's|http://security.ubuntu.com|http://mirrors.aliyun.com|g' /etc/apt/sources.list && \
    apt update && apt install --no-install-recommends openssh-server sudo -y
RUN groupadd juicedata && useradd -ms /bin/bash -g juicedata juicedata -u 1024
RUN mkdir /var/jfs
RUN mkdir -p /home/juicedata/.ssh
COPY id_rsa.pub /home/juicedata/.ssh/authorized_keys
RUN chown juicedata:juicedata /home/juicedata/.ssh/authorized_keys && chmod 600 /home/juicedata/.ssh/authorized_keys
RUN service ssh start
EXPOSE 22
CMD ["/usr/sbin/sshd","-D"]
