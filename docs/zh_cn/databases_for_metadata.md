# JuiceFS 支持的元数据存储引擎

通过阅读 [JuiceFS 的技术架构](architecture.md) 和 [JuiceFS 如何存储文件](how_juicefs_store_files.md) 两篇文档，我们已经知道 JuiceFS 被设计成为一种将数据和元数据独立存储的架构，通常来说，数据被存储在以对象存储为主的云存储中，而数据所对应的元数据则被存储在独立的数据库中。

## 元数据存储引擎

对于 JuiceFS 文件系统来说，元数据至关重要。它记录着每一个文件的详细信息，名称、大小、权限、位置等等。特别是这种数据与元数据分离存储的文件系统，元数据的读写性能直接决定了文件系统实际的性能表现。

JuiceFS 的元数据存储采用了多引擎设计。为了打造一个超高性能的云原生文件系统，JuiceFS 最先支持的是运行在内存上的键值数据库—— Redis，这使得 JuiceFS 拥有十倍于 Amazon EFS 和 S3FS 的性能表现。

伴随着社区用户积极参与和沟通反馈，我们了解到有很多应用场景并不绝对依赖超高的性能，有时候用户只是想临时找到一个趁手的工具在云上可靠的迁移数据，或者只是想更简单的把对象存储存储挂载到本地小规模的使用。

在社区用户需求的驱动下，JuiceFS 陆续开放了对 MySQL/MariaDB、PostgreSQL、Sqlite 等更多数据库的支持，并且会在将来开放对更多数据库的支持。

**需要特别注意的是**，在使用 JuiceFS 文件系统的过程中不论你选择哪种数据库存储元数据，最最最重要的是，请 **务必确保元数据的安全！**元数据一旦损坏或丢失，会直接导致对应数据彻底损坏或丢失，严重的可能直接导致整个文件系统发生损毁。

## Redis

> [Redis](https://redis.io/) 是一款开源的（BSD许可）基于内存的数据结构存储，常被用作数据库、缓存和消息代理。

你可以很容易的在云计算平台购买到各种配置的云 Redis 数据库，但如果你只是想要快速评估 JuiceFS，可以使用 Docker 快速的在本地电脑上运行一个 Redis 数据库实例：

```shell
$ sudo docker run -d --name redis \
	-v redis-data:/data \
	-p 6379:6379 \
	--restart unless-stopped \
	redis redis-server --appendonly yes
```

容器创建成功以后，可使用 `redis://127.0.0.1:6379` 访问 redis 数据库。

> **注意**：以上命令将 redis 的数据持久化在 docker 的 redis-data 数据卷当中，你可以按需修改数据持久化的存储位置。

> **安全提示**：以上命令创建的 redis 数据库实例没有启用身份认证，且暴露了主机的 `6379` 端口，如果你要通过互联网访问这个数据库实例，强烈建议参照 [Redis 官方文档](https://redis.io/topics/security) 启用保护模式。

你还可以查看 [Redis 最佳实践](redis_best_practices.md)，进一步了解相关内容。

## MySQL

> MySQL 是世界上最受欢迎的开源关系型数据库之一，常被作为 Web 应用程序的首选数据库。

你可以很容易的在云计算平台购买到各种配置的云 MySQL 数据库，但如果你只是想要快速评估 JuiceFS，可以使用 Docker 快速的在本地电脑上运行一个 MySQL 数据库实例：

```shell
$ sudo docker run -d --name mysql \
	-p 3306:3306 \
	-v mysql-data:/var/lib/mysql \
	-e MYSQL_DATABASE=juicefs \
	-e MYSQL_USER=user \
	-e MYSQL_PASSWORD=password \
	--restart unless-stopped \
	mysql
```

## MariaDB

## Sqlite

## PostgreSQL

