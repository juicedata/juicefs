---
slug: /comparison/juicefs_vs_3fs
description: This article compares the architectures, features, and innovations of DeepSeek 3FS and JuiceFS in AI storage scenarios.
---

# JuiceFS vs. 3FS

3FS (Fire-Flyer File System) is a high-performance distributed file system designed for AI training and inference workloads, open-sourced by DeepSeek. It uses NVMe SSDs and RDMA networks to provide a shared storage layer, optimized for the demanding I/O requirements of large-scale AI applications.

JuiceFS is a cloud-native distributed file system that stores data in object storage. The Community Edition, open-sourced on GitHub in 2021, integrates with multiple metadata services and supports diverse use cases. The Enterprise Edition, tailored for high-performance scenarios, is widely adopted in large-scale AI tasks, including generative AI, autonomous driving, quantitative finance, and biotechnology.

This document provides a comprehensive comparison between 3FS and JuiceFS in terms of architecture, file distribution, RPC framework, and features.

## Architecture comparison

### 3FS

3FS employs an architecture designed for AI workloads with the following key components:

- **Cluster manager**: Handles node membership changes and distributes cluster configurations to other components. Multiple cluster managers are deployed with one elected as primary for high availability.
- **Metadata service**: Stateless services that handle file metadata operations, backed by FoundationDB—a transactional key-value database for storing metadata.
- **Storage service**: Manages data storage using local NVMe SSDs with CRAQ (Chain Replication with Apportioned Queries) for data consistency.
- **Clients**: Provides both FUSE client for POSIX compatibility and native client API for high-performance zero-copy operations.

All components communicate via RDMA for networking. Cluster configurations are stored in reliable distributed services like ZooKeeper or etcd.

![3FS architecture](https://static1.juicefs.com/images/3FS_JiaGou.original.png)

### JuiceFS

JuiceFS uses a modular, cloud-native architecture that comprises three core components:

- **Metadata engine**: Stores file metadata, including standard file system metadata and file data indexes. The Community Edition supports various databases including Redis, TiKV, MySQL, PostgreSQL, and FoundationDB. The Enterprise Edition uses a self-developed distributed metadata service.
- **Data storage**: Generally an object storage service, which can be public cloud object storage or on-premises deployed object storage service. Supports integration with various storage backends.
- **JuiceFS client**: Provides different access methods such as POSIX (FUSE), Hadoop SDK, CSI Driver, and S3 Gateway.

![JuiceFS Community Edition architecture](../../images/juicefs-arch.svg)

### Architectural differences

#### Storage module

3FS employs local NVMe SSDs for data storage and utilizes the CRAQ (Chain Replication with Apportioned Queries) algorithm to ensure data consistency. Replicas are organized into a chain where write requests start from the head and propagate sequentially to the tail. A write operation is confirmed only after reaching the tail. For read requests, any replica in the chain can be queried.

![CRAQ consistency algorithm](https://static1.juicefs.com/images/CRAQ_YiZhiXingSuanFa.original.png)

While this design introduces higher write latency due to sequential propagation, it prioritizes read performance, which is crucial for read-intensive AI workloads.

In contrast, JuiceFS uses object storage as its data storage solution, inheriting key advantages such as data reliability and consistency. The storage module provides standard object operation interfaces (GET/PUT/HEAD/LIST), enabling seamless integration with various storage backends. JuiceFS Community Edition provides local cache for AI scenario bandwidth requirements, while the Enterprise Edition uses distributed cache for larger aggregate read bandwidth needs.

#### Metadata module

In 3FS, file attributes are stored as key-value pairs within a stateless, high-availability metadata service, backed by FoundationDB. FoundationDB ensures global ordering of keys and evenly distributes data across nodes via consistent hashing. To optimize directory listing efficiency, 3FS constructs dentry keys by combining a "DENT" prefix with the parent directory's inode number and file name.

JuiceFS Community Edition provides a metadata module that offers a set of interfaces for metadata operations, supporting integration with various metadata services including key-value databases (Redis, TiKV), relational databases (MySQL, PostgreSQL), and FoundationDB. The Enterprise Edition employs a proprietary high-performance metadata service that dynamically balances data and hot operations based on workload patterns.

#### Client

3FS provides both a FUSE client and a native client API to bypass FUSE for direct data operations. The native client eliminates data copying introduced by the FUSE layer, reducing I/O latency and memory bandwidth overhead through zero-copy communication using shared memory and semaphores.

![3FS native client API](https://static1.juicefs.com/images/3FS_NATIVE_Client_API.original.png)

3FS uses `hf3fs_iov` to store shared memory attributes and `IoRing` for inter-process communication. The system creates virtual files and uses semaphores to facilitate communication between the user process and FUSE process.

JuiceFS' FUSE client offers a more comprehensive implementation with features such as:

- Immediate file length updates after successful object upload
- BSD locks (flock) and POSIX locks (fcntl)
- Advanced interfaces like `file_copy_range`, `readdirplus`, and `fallocate`

Beyond the FUSE client, JuiceFS Community Edition also provides Java SDK, Python SDK, S3 Gateway, and CSI Driver for user-space execution, with the Enterprise Edition offering additional enterprise-grade features.

## File distribution comparison

### 3FS file distribution

3FS uses fixed-size chunks, allowing clients to calculate which chunks an I/O request targets based on the file inode and request offset/length, avoiding database queries for each I/O operation. The chunk index is obtained through `offset/chunk_size`, and the chain index through `chunk_id%stripe`.

To address data imbalance, the first chain of each file is selected in a round-robin manner. When a file is created, chains are randomly sorted and stored in metadata.

![3FS file distribution](https://static1.juicefs.com/images/3FS_WenJianFenBu.original.png)

### JuiceFS file distribution

JuiceFS manages data blocks according to chunk, slice, and block rules. Each chunk is fixed at 64MB for optimizing data search and positioning. Actual file write operations are performed on slices, which represent continuous write processes within chunks. Blocks (default 4MB) are the basic unit of physical storage in object storage and disk cache.

![JuiceFS file distribution](../../images/file-and-chunks.svg)

Slice is a unique structure in JuiceFS that records file write operations and persists them in object storage. Since object storage doesn't support in-place file modification, JuiceFS uses slices to update file content without rewriting entire files. All slices are written once, reducing reliance on underlying object storage consistency and simplifying cache system complexity.

## 3FS RPC framework

3FS implements a custom RPC framework using RDMA as the underlying network communication protocol, which JuiceFS currently doesn't support. The framework provides capabilities such as serialization and packet merging, using templates to implement reflection for data structure serialization.

![3FS FUSE client RPC process](https://static1.juicefs.com/images/3FS_FUSE_Client_DiaoYong_MetadataFuWuDe_RPC_Guo.original.png)

The 3FS cache system consists of TLS (Thread-Local Storage) queues and global queues. Memory allocation from TLS queues requires no locks, while global queue access requires locking. Multiple RPC requests may be merged into one InfiniBand request for efficiency.

## Feature comparison

| Features | 3FS | JuiceFS Community | JuiceFS Enterprise |
|----------|-----|-------------------|-------------------|
| Metadata | Stateless metadata service + FoundationDB | External database | Self-developed high-performance distributed metadata engine (horizontally scalable) |
| Data storage | Self-managed | Object storage | Object storage |
| Redundancy | Multi-replica | Provided by object storage | Provided by object storage |
| Data caching | None | Local cache | Self-developed high-performance multi-copy distributed cache |
| Encryption | Not supported | Supported | Supported |
| Compression | Not supported | Supported | Supported |
| Quota management | Not supported | Supported | Supported |
| Network protocol | RDMA | TCP | TCP |
| Snapshots | Not supported | Supports cloning | Supports cloning |
| POSIX ACL | Not supported | Supported | Supported |
| POSIX compliance | Partial | Fully compatible | Fully compatible |
| CSI Driver | No official support | Supported | Supported |
| Clients | FUSE + native client | POSIX (FUSE), Java SDK, Python SDK, S3 Gateway | POSIX (FUSE), Java SDK, S3 Gateway, Python SDK |
| Multi-cloud mirroring | Not supported | Not supported | Supported |
| Cross-cloud/region replication | Not supported | Not supported | Supported |
| Main maintainer | DeepSeek | Juicedata | Juicedata |
| Development language | C++, Rust (local storage engine) | Go | Go |
| License | MIT | Apache License 2.0 | Commercial |

## Summary

For large-scale AI training, 3FS adopts a performance-first design approach:

- **Local storage**: Uses local NVMe SSDs, requiring users to manage underlying storage infrastructure
- **Zero-copy optimization**: Achieves zero-copy from client to NIC, reducing I/O latency and memory bandwidth usage via shared memory and semaphores
- **RDMA networking**: Leverages RDMA for better networking performance
- **Optimized I/O**: Enhances small I/O and metadata operations with TLS-backed I/O buffer pools and merged network requests

While this approach can deliver performance improvements, it comes with higher costs and greater maintenance complexity.

JuiceFS uses object storage as its backend, significantly reducing costs and simplifying maintenance. To meet AI workloads' performance demands:

- **Enterprise Edition features**: Distributed caching, distributed metadata service, and Python SDK
- **Upcoming optimizations**: v5.2 adds zero-copy over TCP for faster data transfers
- **Cloud-native advantages**: Full POSIX compatibility, mature open-source ecosystem, and Kubernetes CSI support
- **Enterprise capabilities**: Quotas, security management, and disaster recovery features
