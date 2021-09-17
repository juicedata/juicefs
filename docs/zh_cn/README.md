# JuiceFS 用户手册

[![license](https://img.shields.io/badge/license-AGPL%20V3-blue)](https://github.com/juicedata/juicefs/blob/main/LICENSE) [![Go Report](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/badge/github.com/juicedata/juicefs) [![Join Slack](https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack)](https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-0bJojPaql8cfFgwerDQJgA)

![JuiceFS LOGO](../images/juicefs-logo.png)

JuiceFS 是一款高性能 [POSIX](https://en.wikipedia.org/wiki/POSIX) 文件系统，针对云原生环境特别优化设计，在 GNU Affero General Public License v3.0 开源协议下发布。使用 JuiceFS 存储数据，数据本身会被持久化在对象存储（例如，Amazon S3），而数据所对应的元数据可以根据场景需求被持久化在 Redis、MySQL、SQLite 等多种数据库引擎中。JuiceFS 可以简单便捷的将海量云存储直接接入已投入生产环境的大数据、机器学习、人工智能以及各种应用平台，无需修改代码即可像使用本地存储一样高效使用海量云端存储。

## 核心特性

1. **POSIX 兼容**：像本地文件系统一样使用，无缝对接已有应用，无业务侵入性；
2. **HDFS 兼容**：完整兼容 [HDFS API](hadoop_java_sdk.md)，提供更强的元数据性能；
3. **S3 兼容**：提供 [S3 网关](s3_gateway.md) 实现 S3 协议兼容的访问接口；
4. **云原生**：通过 [Kubernetes CSI Driver](how_to_use_on_kubernetes.md) 可以很便捷地在 Kubernetes 中使用 JuiceFS；
5. **多端共享**：同一文件系统可在上千台服务器同时挂载，高性能并发读写，共享数据；
6. **强一致性**：确认的修改会在所有挂载了同一文件系统的服务器上立即可见，保证强一致性；
7. **强悍性能**：毫秒级的延迟，近乎无限的吞吐量（取决于对象存储规模），查看[性能测试结果](benchmark.md)；
8. **数据安全**：支持传输中加密（encryption in transit）以及静态加密（encryption at rest），[查看详情](encrypt.md)；
9. **文件锁**：支持 BSD 锁（flock）及 POSIX 锁（fcntl）；
10. **数据压缩**：支持使用 [LZ4](https://lz4.github.io/lz4) 或 [Zstandard](https://facebook.github.io/zstd) 压缩数据，节省存储空间；

## 目录

- 介绍
  - [JuiceFS 是什么？](introduction.md)
  - [JuiceFS 的技术架构](architecture.md)
  - [JuiceFS 如何存储文件？](how_juicefs_store_files.md)
  - [JuiceFS 支持的对象存储](how_to_setup_object_storage.md)
  - [JuiceFS 支持的元数据存储引擎](databases_for_metadata.md)
- [快速上手](quick_start_guide.md)
- 基本用法
  - [Linux 系统使用 JuiceFS](juicefs_on_linux.md)
  - [Windows 系统使用 JuiceFS](juicefs_on_windows.md)
  - [macOS 系统使用 JuiceFS](juicefs_on_macos.md)
  - [Docker 使用 JuiceFS](juicefs_on_docker.md)
  - [Kubernetes 使用 JuiceFS](how_to_use_on_kubernetes.md)
  - [K3s 使用 JuiceFS](juicefs_on_k3s.md)
  - [Hadoop 生态使用 JuiceFS 存储](hadoop_java_sdk.md)
  - [JuiceFS 启用 S3 网关](s3_gateway.md)
  - [JuiceFS 客户端编译和升级](client_compile_and_upgrade.md)
- [命令参考](command_reference.md)
- 进阶主题
  - [Redis 最佳实践](redis_best_practices.md)
  - [JuiceFS 性能测试](benchmark.md)
  - [JuiceFS 元数据引擎对比测试](metadata_engines_benchmark.md)
  - [JuiceFS 元数据备份和恢复](metadata_dump_load.md)
  - [数据加密](encrypt.md)
  - [POSIX 兼容性](posix_compatibility.md)
  - [LTP 兼容性测试](LTP_compatibility_test.md)
  - [JuiceFS 缓存管理](cache_management.md)
  - [JuiceFS 性能诊断](operations_profiling.md)
  - [JuiceFS 性能统计监控](stats_watcher.md)
  - [JuiceFS 故障诊断和分析](fault_diagnosis_and_analysis.md)
  - [JuiceFS 监控指标](p8s_metrics.md)
  - [FUSE 挂载选项](fuse_mount_options.md)
  - [JuiceFS 多主机间同步账户](sync_accounts_between_multiple_hosts.md)
  - [同类技术对比](comparison_with_others.md)
  - [用量统计](usage-tracking.md)
- [应用场景&案例](case.md)
- [常见问题](faq.md)
- [发行注记](release_notes.md)
- [术语表](glossary.md)
