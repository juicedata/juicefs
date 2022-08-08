# JuiceFS 用户手册

[![license](https://img.shields.io/badge/license-Apache%20v2.0-blue)](https://github.com/juicedata/juicefs/blob/main/LICENSE) [![Go Report](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/badge/github.com/juicedata/juicefs) [![Join Slack](https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack)](https://go.juicefs.com/slack)

![JuiceFS LOGO](images/juicefs-logo.svg)

JuiceFS 是一款高性能 [POSIX](https://en.wikipedia.org/wiki/POSIX) 文件系统，针对云原生环境特别优化设计，在 Apache 2.0 开源协议下发布。使用 JuiceFS 存储数据，数据本身会被持久化在对象存储（例如，Amazon S3），而数据所对应的元数据可以根据场景需求被持久化在 Redis、MySQL、SQLite 等多种数据库引擎中。JuiceFS 可以简单便捷的将海量云存储直接接入已投入生产环境的大数据、机器学习、人工智能以及各种应用平台，无需修改代码即可像使用本地存储一样高效使用海量云端存储。

## 核心特性

1. **POSIX 兼容**：像本地文件系统一样使用，无缝对接已有应用，无业务侵入性；
2. **HDFS 兼容**：完整兼容 [HDFS API](deployment/hadoop_java_sdk.md)，提供更强的元数据性能；
3. **S3 兼容**：提供 [S3 网关](deployment/s3_gateway.md) 实现 S3 协议兼容的访问接口；
4. **云原生**：通过 [Kubernetes CSI Driver](deployment/how_to_use_on_kubernetes.md) 可以很便捷地在 Kubernetes 中使用 JuiceFS；
5. **多端共享**：同一文件系统可在上千台服务器同时挂载，高性能并发读写，共享数据；
6. **强一致性**：确认的修改会在所有挂载了同一文件系统的服务器上立即可见，保证强一致性；
7. **强悍性能**：毫秒级的延迟，近乎无限的吞吐量（取决于对象存储规模），查看[性能测试结果](benchmark/benchmark.md)；
8. **数据安全**：支持传输中加密（encryption in transit）以及静态加密（encryption at rest），[查看详情](security/encrypt.md)；
9. **文件锁**：支持 BSD 锁（flock）及 POSIX 锁（fcntl）；
10. **数据压缩**：支持使用 [LZ4](https://lz4.github.io/lz4) 或 [Zstandard](https://facebook.github.io/zstd) 压缩数据，节省存储空间；
