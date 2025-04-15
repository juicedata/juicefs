FROM centos/python-38-centos7

USER 0

RUN curl -fsSL https://autoinstall.plesk.com/PSA_18.0.62/examiners/repository_check.sh | bash -s -- update >/dev/null && \
    yum install -y make gcc && \
    cd /tmp && \
    curl -L https://static.juicefs.com/misc/go1.20.14.linux-amd64.tar.gz -o go1.20.14.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go1.20.14.linux-amd64.tar.gz && \
    rm go1.20.14.linux-amd64.tar.gz && \
    ln -s /usr/local/go/bin/go /usr/bin/go && \
    python3 -m pip install --upgrade pip && \
    python3 -m pip install --upgrade setuptools && \
    pip install wheel build 
