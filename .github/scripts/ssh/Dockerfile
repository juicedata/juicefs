FROM ubuntu:latest
RUN apt update && apt install  openssh-server sudo -y
RUN groupadd juicedata && useradd -ms /bin/bash -g juicedata juicedata -u 1024
RUN mkdir /var/jfs
RUN mkdir -p /home/juicedata/.ssh
COPY id_rsa.pub /home/juicedata/.ssh/authorized_keys
RUN chown juicedata:juicedata /home/juicedata/.ssh/authorized_keys && chmod 600 /home/juicedata/.ssh/authorized_keys
RUN service ssh start
EXPOSE 22
CMD ["/usr/sbin/sshd","-D"]