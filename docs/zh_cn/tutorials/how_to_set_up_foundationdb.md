# 如何配置FoundationDB
## 在单机上配置FoundationDB

**[Ubuntu](https://apple.github.io/foundationdb/getting-started-linux.html)**
```
//下载server和client deb包
wget https://github.com/apple/foundationdb/releases/download/6.3.24/foundationdb-clients_6.3.24-1_amd64.deb
wget https://github.com/apple/foundationdb/releases/download/6.3.24/foundationdb-server_6.3.24-1_amd64.deb
//安装
sudo dpkg -i foundationdb-clients_6.3.24-1_amd64.deb \
foundationdb-server_6.3.24-1_amd64.deb
```
**[RHEL/CentOS6/CentOS7](https://apple.github.io/foundationdb/getting-started-linux.html)**
```
//下载server和client rpm包
wget https://github.com/apple/foundationdb/releases/download/6.3.24/foundationdb-clients-6.3.24-1.el7.x86_64.rpm
wget https://github.com/apple/foundationdb/releases/download/6.3.24/foundationdb-server-6.3.24-1.el7.x86_64.rpm
//安装
sudo rpm -Uvh foundationdb-clients-6.3.24-1.el7.x86_64.rpm \
foundationdb-server-6.3.24-1.el7.x86_64.rpm
```
**[macOS](https://apple.github.io/foundationdb/getting-started-linux.html)**

详情请移步foundationdb官网

## [在多台机器上配置foundationdb集群](https://apple.github.io/foundationdb/administration.html#adding-machines-to-a-cluster)
> 部署单台机器的步骤与上述一致。
- 首先在每台机器上部署好单个foundationdb
- 选择一个节点将其fdb.cluster文件修改（路径默认/etc/foundationdb/fdb.cluster），此文件由一行字符串组成，格式为description:ID@IP:PORT,IP:PORT,...，仅添加其他机器的IP:PORT即可。
- 将此修改完的fdb.cluster拷贝到其他节点
- 将机器重启（sudo service foundationdb restart）