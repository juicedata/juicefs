# JuiceFS User Manual

[![license](https://img.shields.io/badge/license-AGPL%20V3-blue)](https://github.com/juicedata/juicefs/blob/main/LICENSE) [![Go Report](https://img.shields.io/badge/go%20report-A+-brightgreen.svg?style=flat)](https://goreportcard.com/badge/github.com/juicedata/juicefs) [![Join Slack](https://badgen.net/badge/Slack/Join%20JuiceFS/0abd59?icon=slack)](https://join.slack.com/t/juicefs/shared_invite/zt-n9h5qdxh-0bJojPaql8cfFgwerDQJgA)

![JuiceFS LOGO](../images/juicefs-logo.png)

JuiceFS is a high-performance [POSIX](https://en.wikipedia.org/wiki/POSIX) file system released under GNU Affero General Public License v3.0. It is specially optimized for the cloud-native environment. Using the JuiceFS  to store data, the data itself will be persisted in object storage (e.g. Amazon S3), and the metadata corresponding to the data can be persisted in various database engines such as Redis, MySQL, and SQLite according to the needs of the scene.

JuiceFS can simply and conveniently connect massive cloud storage directly to big data, machine learning, artificial intelligence, and various application platforms that have been put into production environment, without modifying the code, you can use massive cloud storage as efficiently as using local storage.

## Highlighted Features

1. **Fully POSIX-compatible**: Use like a local file system, seamlessly docking with existing applications, no business intrusion.
2. **Fully Hadoop-compatible**: JuiceFS [Hadoop Java SDK](hadoop_java_sdk.md) is compatible with Hadoop 2.x and Hadoop 3.x. As well as variety of components in Hadoop ecosystem.
3. **S3-compatible**:  JuiceFS [S3 Gateway](s3_gateway.md) provides S3-compatible interface.
4. **Cloud Native**: JuiceFS provides [Kubernetes CSI driver](how_to_use_on_kubernetes.md) to help people who want to use JuiceFS in Kubernetes.
5. **Sharing**: JuiceFS is a shared file storage that can be read and written by thousands clients.
6. **Strong Consistency**: The confirmed modification will be immediately visible on all servers mounted with the same file system .
7. **Outstanding Performance**: The latency can be as low as a few milliseconds and the throughput can be expanded to nearly unlimited. [Test results](benchmark.md)
8. **Data Encryption**: Supports data encryption in transit and at rest, read [the guide](encrypt.md) for more information.
9. **Global File Locks**: JuiceFS supports both BSD locks (flock) and POSIX record locks (fcntl).
10. **Data Compression**: JuiceFS supports use [LZ4](https://lz4.github.io/lz4) or [Zstandard](https://facebook.github.io/zstd) to compress all your data.

## Table of content

- **Introduction**
  - [What is JuiceFS](introduction.md)
  - [JuiceFS technical architecture](architecture.md)
  - [How JuiceFS store files](how_juicefs_store_files.md)
  - [How to setup object storage](how_to_setup_object_storage.md)
  - [Metadata engines for JuiceFS](databases_for_metadata.md)
- [Quick Start Guide](quick_start_guide.md)
- **Basic Usage**
  - [Use JuiceFS on Linux](juicefs_on_linux.md)
  - [Use JuiceFS on macOS](juicefs_on_macos.md)
  - [Use JuiceFS on Windows](juicefs_on_windows.md)
  - [Use JuiceFS on Docker](juicefs_on_docker.md)
  - [Use JuiceFS on Kubernetes](how_to_use_on_kubernetes.md)
  - [Use JuiceFS on K3s](juicefs_on_k3s.md)
  - [Use JuiceFS on Hadoop ecosystem](hadoop_java_sdk.md)
  - [JuiceFS enable S3 Gateway](s3_gateway.md)
  - [JuiceFS client compilation and upgrade](client_compile_and_upgrade.md)
- [Command Reference](command_reference.md)
- **Advanced Topics**
  - [Redis best practices](redis_best_practices.md)
  - [JuiceFS benchmark](benchmark.md)
  - [JuiceFS metadata engines benchmark](metadata_engines_benchmark.md)
  - [JuiceFS metadata backup and recovery](metadata_dump_load.md)
  - [Data encryption](encrypt.md)
  - [POSIX compatibility](posix_compatibility.md)
  - [JuiceFS cache management](cache_management.md)
  - [JuiceFS operations profiling](operations_profiling.md)
  - [JuiceFS performance statistics watcher](stats_watcher.md)
  - [JuiceFS fault diagnosis and analysis](fault_diagnosis_and_analysis.md)
  - [JuiceFS metrics](p8s_metrics.md)
  - [FUSE mount options](fuse_mount_options.md)
  - [JuiceFS sync accounts between multiple hosts](sync_accounts_between_multiple_hosts.md)
  - [Comparison with others](comparison_with_others.md)
  - [Usage tracking](usage_tracking.md)
- [Scenarios & Cases](case.md)
- [FAQ](faq.md)
- [Release Notes](release_notes.md)
- [Glossary](glossary.md)
