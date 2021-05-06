# 用 JuiceFS 备份 NGINX 日志

生产环境中的 NGINX 经常作为反向代理，配置多台，用来对接后面的各种应用服务。日志主要有两类，访问日志 (access.log) 和错误日志 (error.log)。

日志是分散在每个 NGINX 节点的磁盘上的，每台机器自己的磁盘并不安全，而且分散的日志也难以维护和使用。所以，我们都会将日志汇总在一个更靠谱的存储系统中，一方面长期存储安全可靠，一方面也方便做分析使用。

在日志的存储上需要里，容量扩展性强，稳定安全，方便运维操作，价格便宜，最好按使用量付费是重点，对于存储性能的要求会低一些。目前常用的有 NFS、HDFS、对象存储等，把这些存储与 JuiceFS 做个比较：

![_images/storage_comparison.png](https://juicefs.com/docs/zh/_images/storage_comparison.png)

说到日志的收集方式，主要有两种：**定时收集** 和 **实时收集**，我们在下面分别说明。JuiceFS 使用客户自己的对象存储保存文件数据，所以也自然继承了对象存储的好处，在此之上，我们提供了高性能的元数据服务和完整的 POSIX 兼容，使用上又比对象存储便利的多。

## 定时收集

通常按照小时、天，把日志拷贝到一个统一的存储点。这方面的工具集很多，我们用 Linux 默认安装的 logrotate 举例说明。

首先，要 [注册个账号](https://juicefs.com/accounts/register)，并创建了一个文件系统，假设叫 super-backup。

第一步，每台机器安装 JuiceFS 客户端，挂载到 /jfs。

下载 JuiceFS 客户端

```
curl -L juicefs.com/static/juicefs -o juicefs && chmod +x juicefs
```

挂载文件系统

```
sudo ./juicefs mount super-backup /jfs
```

在自动化配置管理中使用 JuiceFS 也同样方便，具体方法请在上手指南中查看 [如何通过命令行认证](https://juicefs.com/docs/zh/getting_started.html#remember-authentication) 和 [开机自动挂载](https://juicefs.com/docs/zh/getting_started.html#mount-on-boot)，我们支持 [Docker 中挂载](https://juicefs.com/docs/zh/getting_started.html#mount-in-docker) 和 [Kubernates 中挂载](https://juicefs.com/docs/zh/use_juicefs_in_kubernetes.html)。

第二步，在每台机器上用 logrotate 配置日志的滚动策略，修改 `/etc/logrotate.d/nginx`

```
/var/log/nginx/*.log {
    daily    # 每天滚动一次
    compress
    dateext # 把日期添加到文件名中
    sharedscripts
    postrotate
        [ -f /var/run/nginx.pid ] && kill -USR1 `cat /var/run/nginx.pid` # 重新加载日志文件
    endscript
    lastaction
        rsync -au /your/nginx-log/path/*.gz /jfs/nginx-logs/`hostname -s`/ # 把压缩好的日志同步到 JuiceFS
    endscript
}
```

到此，NGINX 日志就可以每天 rotate 并保存到 JuiceFS 中了。增加 NGINX 节点时，只需要在新增节点上做同样的配置即可。

如果使用 NFS，在 logrotate 中的配置是基本一样的。但是 NFS 有几个不足之处：

- 大部分 NFS 存在单点故障，而 JuiceFS 是高可用的（专业版承诺 99.95% SLA）。
- NFS 协议传输不加密，所以你需要保证 NFS 和 NGINX 在同一个 VPC 中，如果还有其他要备份的服务，部署上就很麻烦。JuiceFS 传输有 SSL 加密，不受 VPC 限制。
- NFS 需要事先容量规划，JuiceFS 是弹性扩容，按容量付费的，更省心，更便宜。
- 如果使用 HDFS 或者对象存储，日后访问备份数据时，就比较麻烦。JuiceFS 就简单很多，比如可以直接用 zgrep 查询。

再分享几个 Tips：

1. 执行 `logrotate -f /etc/logrotate.d/nginx` 立即执行对 logrotate 配置做个验证。还可以用 -d 做调试。
2. Logrotate 基于 cron 运行，无论你设置 weekly、daily 还是 hourly，具体的执行时间可以在 /etc/crontab 中修改。
3. 如果你觉得日志文件太多，我们还提供了 `juicefs merge` 命令可以快速合并 gzip 压缩过的日志文件。

说完定时汇总，下一节我们再说说日志实时收集。

## 实时收集

日志的实时收集已经有了很多开源工具，常用的有 [Logstash](https://www.elastic.co/products/logstash)、[Flume](https://flume.apache.org/)、[Scribe](https://github.com/facebookarchive/scribe)、[Kafka](https://kafka.apache.org/) 等。

在集群不是很大的时候，日志收集、分析、索引、展示有个全家桶方案 ELK，其中用 Logstash 做日志收集和分析。

需要下面的部署方式：

1. 在每台机器上部署一个 Logstash Agent（Flume 等其他工具同理）；
2. 部署一个 Logstash Central 做日志汇总；
3. 部署一个 Redis 做整个服务的 Broker，目的是在日志收集和写入中间做个缓冲，避免 Central 挂了导致日志丢失；
4. 然后再配置 Central 的落盘方式，将日志存储到 JuiceFS / NFS / 对象存储 / HDFS 等。

先看看架构图：

![_images/logstash_on_juicefs.png](https://juicefs.com/docs/zh/_images/logstash_on_juicefs.png)

这里不讲 Logstash 在收集、分析、过滤环节的配置了，网络上有很多文章可查（比如：[Logstash 最佳实践](https://doc.yonyoucloud.com/doc/logstash-best-practice-cn/index.html)），说一下输出环节。

把 Logstash 收集处理好的日志保存到 JuiceFS 只要在配置的 output 部分设置一下：

```
output {
    file {
        path => "/jfs/nginx-logs/%{host}-%{+yyyy/MM/dd/HH}.log.gz"
        message_format => "%{message}"
        gzip => true
    }
}
```

存储到 NFS 也可以用上面的配置，**缺点和上文定时收集部分提到的相同**。

如果要保存到对象存储、HDFS，需要再配置 Logstash 的第三方插件，大部分是非官方的，随着 Logstash 版本的升级，使用时可能需要折腾一下。

## 最简单的实时收集方案

其实还有更简单的实时日志收集方法，就是直接让 NGINX 把日志输出到 JuiceFS 中，省去了维护和部署日志收集系统的麻烦。使用这个方案可能会担心 JuiceFS 出问题时影响 NGINX 的正常运行，有两方面可以帮大家减少一些顾虑：

1. JuiceFS 本身是一个高可用的服务，专业版承诺 99.95% 的可用性，应该跟你的数据库等服务在一个可用性级别；
2. NGINX 的日志输出是使用异步 IO 来实现的，即使 JuiceFS 出现暂时性的抖动，也基本不影响 NGINX 的正常运行（restart 或者 reload 可能会受影响）。

如果不喜欢运维复杂的日志收集系统，这个方案值得一试。

## 给 NGINX 日志加一份异地备份

定时收集和实时收集都讲完了，在 super-backup 中存储的 NGINX 日志如何做个 **异地备份** 呢？

只要两步：

一、去 JuiceFS 网站控制台中，访问你文件系统的设置菜单，勾选 “启动复制”，然后选择你要复制到的对象存储，保存。

二、在所有挂载 super-backup 的机器上重新挂载 super-backup 即可。之后新写入的数据会很快同步到要复制的 Bucket 中，旧的数据也会在客户端定时扫描（默认每周一次）时同步。

这样可以全自动的在另外一个对象存储中同步一份数据，有效防止单一对象存储的故障或者所在区域的灾难。

你一定会问：JuiceFS 挂了怎么办？元数据访问不了，光有对象存储里的数据也没用啊。

**JuiceFS 兼容模式**，所有写入的文件会按原样保存在对象存储中，脱离 JuiceFS 的元数据服务，也仍然可以访问里面的文件。完全不用担心 JuiceFS Lock-in。