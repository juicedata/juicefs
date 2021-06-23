FROM centos:7

RUN yum install -y java-1.8.0-openjdk maven git gcc make \
  && ln -s /go/bin/go /usr/local/bin/go \
  && rm -rf /var/cache/yum
