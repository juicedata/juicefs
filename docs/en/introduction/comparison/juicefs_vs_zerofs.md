---
slug: /comparison/juicefs_vs_zerofs
description: This document compares ZeroFS and JuiceFS, examining their product positioning, architecture, and features.
---

# JuiceFS vs. ZeroFS

[ZeroFS](https://github.com/Barre/ZeroFS) is an open-source log-structured file system that turns S3-compatible buckets
into POSIX file systems, exposed over NFS and 9P, or as raw block devices over NBD. It keeps both file data and metadata
in the object store itself and relies on a local disk/memory cache for performance. ZeroFS is dual-licensed under AGPLv3
and a commercial license.

Both ZeroFS and [JuiceFS](https://github.com/juicedata/juicefs) turn object storage into a POSIX file system without
mapping one file to one object, but they differ substantially in metadata architecture, concurrency model, encryption
defaults, and licensing.

## Product positioning

**ZeroFS** is a self-hosted, encrypted-by-default file system for putting POSIX semantics (or a ZFS pool, or a block
device) on top of an S3-compatible bucket. A single ZeroFS core service handles metadata, data, and every protocol
endpoint, so there's no separate metadata database to run alongside it. It targets teams that want to self-host
object-storage-backed file access with a reasonable operational footprint rather than teams that need to scale massive
throughput horizontally. See [Architecture](#architecture) below for how its single-leader design and mandatory
encryption shape that trade-off.

**JuiceFS** is a cloud-native distributed file system designed for demanding AI/ML training, high-performance computing,
and big data analytics across multiple clouds. By using a data-metadata-separation design with a pluggable,
independently scalable metadata engine, JuiceFS supports concurrent access from massive numbers of clients, which is
necessary for large-scale, performance-driven, POSIX-compliant workloads.

## Architecture

**ZeroFS** splits files into 32 KiB extents, which are compressed (Zstd by default, or LZ4) and encrypted
(XChaCha20-Poly1305, without an unencrypted mode), then packed into immutable 256 MiB segments uploaded as individual
objects. The metadata lives in an LSM tree that is itself persisted to the same object store, so ZeroFS has no external
database dependency. Because files are packed into opaque segments rather than mapped 1:1 to objects, ZeroFS files are
not individually accessible through the S3 API, which is similar to JuiceFS' approach in this regard.

Key architectural characteristics:

- No separate metadata database: both metadata (LSM tree) and data (segments) live in the same object store, managed by
  the ZeroFS server. When working with ZeroFS, this server (and its standby for HA and read replicas for read scaling)
  is the main service you deploy and operate. ZeroFS folds metadata management into it instead of requiring a separate
  metadata database.
- One server process (the leader) is the sole writer to the backing object store. It accepts connections from concurrent
  clients and arbitrates their writes and POSIX locks internally, but every mutation is funneled through that single
  process. See [Concurrency and write scalability](#concurrency-and-write-scalability) below.
- Encryption and compression are mandatory on every block, with no option to disable them.
- A dual-tier local cache (encrypted raw-parts cache for object bytes, plaintext decoded-block cache for metadata, plus
  an in-memory tail cache for each inode's most recent extent) reduces round trips to object storage.
- High availability is a two-node leader/standby pair with automatic failover; any number of additional read-only
  replicas can be added for read scaling, but there is exactly one writer process at a time.

**JuiceFS** adopts a decoupled architecture that separates metadata management from data storage. Files are split into
chunks (default 64 MiB, further divided into 4 MiB blocks) before being uploaded to object storage, with corresponding
metadata stored in a separate, pluggable database engine.

![JuiceFS-arch](../../images/juicefs-arch.svg)

Key architectural characteristics:

- JuiceFS supports pluggable metadata engines (Redis, TiKV, MySQL, PostgreSQL, SQLite, etcd, FoundationDB, etc.), each
  with its own operational and HA characteristics.
- JuiceFS stores files using data chunking, which enables efficient partial updates, appends, and high-throughput
  operations. For more details, see [Architecture](../../introduction/architecture.md).
- No dependency on any proprietary intermediate storage format. JuiceFS supports a flexible caching mechanism to
  decrease latency and improve performance.
- Multi-cloud and hybrid cloud support
  with [all mainstream object storage backends](../../reference/how_to_set_up_object_storage.md).

### Data path

**ZeroFS** relays all file data through its server node. Neither the NFS and 9P protocol clients nor the native Linux
kernel client talk to object storage directly, only the ZeroFS server holds the storage credentials and issues the
GET/PUT calls. So reads and writes are proxied through that one process on its way to or from the bucket.

**JuiceFS** clients, on the other hand, directly issue GET/PUT calls to the object store themselves. Metadata operations
go through the metadata engine, but file data always travels directly between the client mounting machines and the
object store, without an intermediate relay.

This distinction carries considerations in cloud deployments. Because ZeroFS clients never reach the object store
themselves, file data effectively crosses the network twice: once between the object store and the ZeroFS server, and
again between the server and the client. When the server, its clients, and the object store don't all sit in the same
network zone, that extra hop can add meaningful data-transfer charges and consumes the server's bandwidth, on top of
whatever the object store would have charged for direct client access. JuiceFS clients talk to object storage directly,
so aside from metadata-engine traffic, there is no equivalent relay hop or its associated data transfer cost to budget
for.

### Concurrency and write scalability

**ZeroFS** does allow many concurrent clients to write — the Kubernetes CSI Driver presents genuine `ReadWriteMany`
volumes, and any number of pods on any number of nodes can mount over 9P and have their POSIX byte-range locks
arbitrated by the leader. The constraint is at the server layer, not the client layer: every one of those clients'
writes is funneled through a single leader process before it reaches object storage, and that process cannot be
horizontally scaled — the HA pair exists to provide failover, not write scale-out. If the leader becomes a bottleneck,
the fix is a bigger leader, not more leaders; the standby stays synchronized as a hot backup, and read replicas serve
reads with bounded staleness (the writer's flush interval, default 30 seconds, plus up to a 10-second replica poll
window) rather than adding write capacity.

**JuiceFS** has no single-writer bottleneck: every client, whether mounted via FUSE, the S3 Gateway, or an SDK, reads
and writes directly against object storage and a shared metadata engine, with strong consistency coordinated by that
engine. Metadata engines bring their own scale-out story — for example TiKV's Raft-replicated cluster, Redis
Cluster/Sentinel, or JuiceFS Enterprise's proprietary distributed metadata engine, which is horizontally scalable and
Raft-replicated across at least 3 copies — so both metadata throughput and the number of concurrent writers can grow
with the workload.

### Caching

**ZeroFS** implements a dual-tier local cache split between disk and memory budgets (`disk_size_gb` / `memory_size_gb`
in its configuration). A raw-parts cache stores object bytes exactly as fetched — still compressed and encrypted — at
128 KiB granularity, while a separate decoded-block cache holds plaintext metadata blocks, and an in-memory tail cache
keeps each inode's most recently written extent. An adaptive readahead layer tracks up to 4 concurrent sequential
streams per file, doubling its fetch window (up to 8 MiB) as sequential access continues.

**JuiceFS** implements client-side caching on local SSDs or memory. It defines a default limit for the disk cache of 100
GiB, which users can freely adjust. When cache usage reaches the limit, JuiceFS automatically cleans up using an
LRU-like algorithm, ensuring cache space remains available for subsequent operations. For more details,
see [Cache](../../guide/cache.md).

### Security

**ZeroFS** makes encryption mandatory: every block is compressed and then encrypted with XChaCha20-Poly1305 before it
leaves the client, with no unencrypted mode available. Keys are derived from a user-supplied password (via Argon2id)
that wraps a data-encryption key; losing the password makes the data permanently unrecoverable. This is a strong default
for security-sensitive deployments, but it is not optional and adds a mandatory encrypt/decrypt step on every I/O path.
Its network protocols (NFS, 9P, NBD, RPC, web UI) do not provide transport encryption or client authentication, so the
project recommends binding them to loopback or otherwise restricted networks.

**JuiceFS** supports encryption as an optional, configurable feature rather than a mandatory default, so users can
choose the trade-off between security overhead and raw performance that fits their workload.

### Multi-cloud support

**ZeroFS** connects to AWS S3 and S3-compatible endpoints (such as MinIO and Cloudflare R2), Google Cloud Storage, and
Azure Blob Storage. **JuiceFS** offers a consistent file system interface across a broader set of storage providers —
including HDFS and local disk — and works with any mainstream object storage backend across AWS, Azure, GCP, or private
clouds. See the table below for more details.

## Feature comparison

| Features                 | ZeroFS                                                                 | JuiceFS Community Edition                                | JuiceFS Enterprise Edition                                         |
|--------------------------|------------------------------------------------------------------------|----------------------------------------------------------|--------------------------------------------------------------------|
| Clients                  | NFS, 9P, NBD (block device), native Linux kernel client, Web UI        | POSIX (FUSE), Java SDK, Python SDK, S3 Gateway           | POSIX (FUSE), Java SDK, Python SDK, S3 Gateway                     |
| Metadata storage         | Embedded LSM tree, persisted in the same object store (no external DB) | External database (Redis, TiKV, MySQL, PostgreSQL, etc.) | Horizontally-scalable high-performance distributed metadata engine |
| Metadata redundancy      | Leader/standby pair with automatic failover                            | Depends on the database used                             | At least 3 copies (based on the Raft consensus algorithm)          |
| Concurrent writers       | Many clients, all writes funneled through one leader process           | Many clients, each writing directly to storage           | Many clients, each writing directly to storage                     |
| Data storage             | AWS S3, S3-compatible, Google Cloud Storage, Azure Blob                | Any mainstream object storage                            | Any mainstream object storage                                      |
| Data redundancy          | Provided by object storage                                             | Provided by object storage                               | Provided by object storage                                         |
| Data caching             | Local disk + memory (dual-tier cache)                                  | Local cache                                              | Distributed cache                                                  |
| Encryption               | ✓ Mandatory, always on (XChaCha20-Poly1305)                           | ✓ Supported (optional)                                  | ✓ Supported (optional)                                            |
| Compression              | ✓ Mandatory (Zstd or LZ4)                                             | ✓ Supported                                             | ✓ Supported                                                       |
| Quota management         | ◐ Filesystem-level capacity quota                                      | ✓ Supported                                             | ✓ Supported                                                       |
| POSIX compliance         | ✓ Fully compatible                                                    | ✓ Fully compatible                                      | ✓ Fully compatible                                                |
| POSIX ACL                | Not publicly documented                                                | ✓ Supported                                             | ✓ Supported                                                       |
| Kubernetes CSI           | ✓ Supported (RWO/ROX/RWX)                                             | ✓ Supported                                             | ✓ Supported                                                       |
| Cross-region replication | ◐ Read replicas only (single writer region)                            | ◐ Relies on external service                             | ✓ Supported                                                       |
| Multi-cloud mirroring    | ✕ Not supported                                                       | ✕ Not supported                                         | ✓ Supported                                                       |
| Pricing                  | AGPLv3, or commercial license                                          | Open source and free (Apache License 2.0)                | Commercial license, volume pricing                                 |

## Licensing implications

**ZeroFS** is dual-licensed under the GNU AGPLv3 and a commercial license. Internal-only use does not trigger copyleft
obligations, but hosting or distributing ZeroFS as a network service requires publishing the complete source of the
combined work under AGPLv3 — unless a commercial license is purchased. This is a materially different obligation from a
permissive license, and it is a factor teams need to evaluate before embedding ZeroFS in a product they distribute or
offer as a service.

**JuiceFS Community Edition** is released under the Apache License 2.0, a permissive license with no copyleft or
source-disclosure obligations for internal use, distribution, or hosted services. JuiceFS Enterprise Edition is
available under a separate commercial license for teams that need its distributed metadata engine, distributed cache,
and multi-cloud mirroring.

## Summary

**ZeroFS** is a security-first file system that folds metadata storage into its own server process instead of requiring
a separate database, encrypts and compresses every block by default, and can be mounted over NFS, 9P, or a native Linux
kernel client, or consumed as an NBD block device. It's still a service you deploy and operate. You just don't operate a
second, separate metadata database alongside it. Clients can connect and write concurrently, but every one of those
writes is funneled through a single leader process before it reaches object storage, and that write path is not
horizontally scalable. Its high-availability design (a leader/standby pair plus unlimited read-only replicas) is built
for failover and read scaling, not for adding write throughput. That makes it a good fit for self-hosted deployments
such as CI build caches, kernel builds, home labs, and small-to-medium services, especially where mandatory encryption
and having a single service to operate matter more than scaling the write path itself.

**JuiceFS** is built for the opposite end of the spectrum: large-scale, high-concurrency, multi-writer workloads. By
decoupling metadata from data and supporting pluggable, independently scalable metadata engines (Redis, TiKV, MySQL,
PostgreSQL, SQLite, and more, or the JuiceFS Enterprise Edition proprietary distributed metadata engine), JuiceFS lets
any number of clients read and write the same file system concurrently with strong consistency, across the broadest
range of object storage backends and multiple clouds. It provides a standard POSIX interface through FUSE, a Java API
for Hadoop ecosystems, an S3 Gateway, and a Kubernetes CSI Driver. The JuiceFS Community Edition is released under the
permissive Apache License 2.0. JuiceFS is widely used in big data analytics, AI/ML training, agentic AI tools,
multi-cloud and hybrid cloud deployments, container shared storage, and high-performance computing.
