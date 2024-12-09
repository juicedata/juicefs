---
title: FAQ
slug: /faq
---

## My question is not answered in the documentation

If you can't find an answer in the documentation, please try using the "Ask AI" feature (in the bottom right corner). If the AI assistant's answer helps you or provides a wrong answer, feel free to leave feedback on the response. Alternatively, use the document search feature (in the top right corner) and try searching with different keywords.

If these methods still do not resolve your question, you can join the [JuiceFS Community](https://juicefs.com/en/community) for further assistance.

## General Questions

### What's the difference between JuiceFS and XXX?

See ["Comparison with Others"](introduction/comparison/juicefs_vs_alluxio.md) for more information.

### How to upgrade JuiceFS client?

First unmount JuiceFS volume, then re-mount the volume with newer version client.

### Where is the JuiceFS log?

Different types of JuiceFS clients have different ways to obtain logs. For details, please refer to ["Client log"](administration/fault_diagnosis_and_analysis.md#client-log) document.

### Can JuiceFS directly read files that already exist in object storage?

JuiceFS cannot directly read files that already exist in object storage. Although JuiceFS typically uses object storage as the data storage layer, it is not a tool for accessing object storage in the traditional sense. You can refer to the [technical architecture](introduction/architecture.md) documentation for more details.

If you want to migrate existing data in an object storage bucket to JuiceFS, you can use [`JuiceFS Sync`](guide/sync.md).

### How can I combine multiple servers into a single JuiceFS file system for use?

No, while JuiceFS supports using local disks or SFTP as the underlying storage, it does not interfere with the logical structure management of the underlying storage. If you wish to consolidate storage space from multiple servers, you may consider using MinIO or Ceph to create an object storage cluster, and then create a JuiceFS file system on top of it.

## Metadata Related Questions

### Does support Redis in Sentinel or Cluster-mode as the metadata engine for JuiceFS?

Yes, There is also a [best practice document](administration/metadata/redis_best_practices.md) for Redis as the JuiceFS metadata engine for reference.

## Object Storage Related Questions

### Why doesn't JuiceFS support XXX object storage?

JuiceFS already supported many object storage, please check [the list](reference/how_to_set_up_object_storage.md#supported-object-storage) first. If this object storage is compatible with S3, you could treat it as S3. Otherwise, try reporting issue.

### Why do I delete files at the mount point, but there is no change or very little change in object storage footprint?

The first reason is that you may have enabled the trash feature. In order to ensure data security, the trash is enabled by default. The deleted files are actually placed in the trash and are not actually deleted, so the size of the object storage will not change. trash retention time can be specified with `juicefs format` or modified with `juicefs config`. Please refer to the ["Trash"](security/trash.md) documentation for more information.

The second reason is that JuiceFS deletes the data in the object storage asynchronously, so the space change of the object storage will be slower. If you need to immediately clean up the data in the object store that needs to be deleted, you can try running the [`juicefs gc`](reference/command_reference.mdx#gc) command.

### Why is file system data size different from object storage usage? {#size-inconsistency}

* ["Random write in JuiceFS"](#random-write) produces data fragments, causing higher storage usage for object storage, especially after a large number of overwrites in a short period of time, many fragments will be generated. These fragments continue to occupy space in object storage until they are compacted and released. You shouldn't worry about this because JuiceFS checks for file compaction with every read/write, and cleans up in the client background job. Alternatively, you can manually trigger merges and garbage collection with [`juicefs gc --compact --delete`](./reference/command_reference.mdx#gc).
* If [Trash](./security/trash.md) is enabled, deleted files will be kept for a specified period of time, and then be garbage collected (all carried out in client background job).
* After data fragments are compacted, stale slices will be kept inside Trash as well (not visible to user), following the same expiration settings. To delete this type of data, read [Trash and stale slices](./security/trash.md#gc).
* If compression is enabled (the `--compress` parameter in the [`format`](./reference/command_reference.mdx#format) command, disabled by default), object storage usage may be smaller than the actual file size (depending on the compression ratio of different types of files).
* Different [storage class](reference/how_to_set_up_object_storage.md#storage-class) of the object storage may calculate storage usage differently. The cloud service provider may set the minimum billable size for some storage classes. For example, the [minimum billable size](https://www.alibabacloud.com/help/en/object-storage-service/latest/storage-fees) for Alibaba Cloud OSS IA storage is 64KB. If a file is smaller than 64KB, it will be calculated as 64KB.
* For self-hosted object storage services, for example MinIO, actual data usage is affected by [storage class settings](https://github.com/minio/minio/blob/master/docs/erasure/storage-class/README.md).

### Does JuiceFS support using a directory in object storage as the value of the `--bucket` option?

As of the release of JuiceFS 1.0, this feature is not supported.

### Does JuiceFS support accessing data that already exists in object storage?

As of the release of JuiceFS 1.0, this feature is not supported.

### Is it possible to bind multiple different object storages to a single file system (e.g. one file system with Amazon S3, GCS and OSS at the same time)?

No. However, you can set up multiple buckets associated with the same object storage service when creating a file system, thus solving the problem of limiting the number of individual bucket objects, for example, multiple S3 Buckets can be associated with a single file system. Please refer to [`--shards`](./reference/command_reference.mdx#format) option for details.

## Performance Related Questions

### How is the performance of JuiceFS?

JuiceFS is a distributed file system, the latency of metadata is determined by 1 (reading) or 2 (writing) round trip(s) between client and metadata service (usually 1-3 ms). The latency of first byte is determined by the performance of underlying object storage (usually 20-100 ms). Throughput of sequential read/write could be 50MB/s - 2800MiB/s (see [fio benchmark](benchmark/fio.md) for more information), depends on network bandwidth and how the data could be compressed.

JuiceFS is built with multiple layers of caching (invalidated automatically), once the caching is warmed up, the latency and throughput of JuiceFS could be close to local file system (having the overhead of FUSE).

### Does JuiceFS support random read/write? How? {#random-write}

Yes, including those issued using mmap. Currently JuiceFS is optimized for sequential reading/writing, and optimized for random reading/writing is work in progress. If you want better random read performance, it's best to turn off compression ([`--compress none`](reference/command_reference.mdx#format)).

JuiceFS does not store the original file in the object storage, but splits it into data blocks using a fixed size (4MiB by default), then uploads it to the object storage, and stores the ID of the data block in the metadata engine. When random write happens, the original metadata is marked stale, and then JuiceFS Client uploads the **new data block** to the object storage, then update the metadata accordingly.

When reading the data of the overwritten part, according to the **latest metadata**, it can be read from the **new data block** uploaded during random writing, and the **old data block** may be deleted by the background garbage collection tasks automatically clean up. This shifts complexity from random writes to reads.

Read [JuiceFS Internals](development/internals.md) and [Data Processing Flow](introduction/io_processing.md) to learn more.

### How to copy a large number of small files into JuiceFS quickly?

You could mount JuiceFS with [`--writeback` option](reference/command_reference.mdx#mount-data-cache-options), which will write the small files into local disks first, then upload them to object storage in background, this could speedup coping many small files into JuiceFS.

See ["Write Cache in Client"](guide/cache.md#client-write-cache) for more information.

### Does JuiceFS support distributed cache?

[Distributed cache](https://juicefs.com/docs/cloud/guide/distributed-cache) is supported in our enterprise edition.

## Mount Related Questions

### Can I mount JuiceFS without `root`?

Yes, JuiceFS could be mounted using `juicefs` without root. The default directory for caching is `$HOME/.juicefs/cache` (macOS) or `/var/jfsCache` (Linux), you should change that to a directory which you have write permission.

See ["Read Cache in Client"](guide/cache.md#client-read-cache) for more information.

## Access Related Questions

### What other ways JuiceFS supports access to data besides mount?

In addition to mounting, the following methods are also supported:

- Kubernetes CSI Driver: Use JuiceFS as the storage layer of Kubernetes cluster through the Kubernetes CSI Driver. For details, please refer to ["Use JuiceFS on Kubernetes"](deployment/how_to_use_on_kubernetes.md).
- Hadoop Java SDK: It is convenient to use a Java client compatible with the HDFS interface to access JuiceFS in the Hadoop ecosystem. For details, please refer to ["Use JuiceFS on Hadoop Ecosystem"](deployment/hadoop_java_sdk.md).
- S3 Gateway: Access JuiceFS through the S3 protocol. For details, please refer to ["Deploy JuiceFS S3 Gateway"](./guide/gateway.md).
- Docker Volume Plugin: A convenient way to use JuiceFS in Docker, please refer to ["Use JuiceFS on Docker"](deployment/juicefs_on_docker.md).
- WebDAV Gateway: Access JuiceFS via WebDAV protocol

### Why the same user on host X has permission to access a file in JuiceFS while has no permission to it on host Y?

The same user has different UID or GID on host X and host Y. Use `id` command to show the UID and GID:

```bash
$ id alice
uid=1201(alice) gid=500(staff) groups=500(staff)
```

Read ["Sync Accounts between Multiple Hosts"](administration/sync_accounts_between_multiple_hosts.md) to resolve this problem.

### Does JuiceFS Gateway support advanced features such as multi-user management?

The built-in `gateway` subcommand of JuiceFS does not support functions such as multi-user management, and only provides basic S3 gateway functions. If you need to use these advanced features, please refer to the [documentation](guide/gateway.md).

### Is there currently an SDK available for JuiceFS?

As of the release of JuiceFS 1.0, the community has two SDKs, one is the [Java SDK](deployment/hadoop_java_sdk.md) that is highly compatible with the HDFS interface officially maintained by Juicedata, and the other is the [Python SDK](https://github.com/megvii-research/juicefs-python) maintained by community users.
