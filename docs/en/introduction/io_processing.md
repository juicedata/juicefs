---
title: Data Processing Workflow
sidebar_position: 3
slug: /internals/io_processing
description: This article introduces read and write implementation of JuiceFS, including how it split file into chunks.
---

## Workflow of processing write {#workflow-of-write}

JuiceFS splits large files at multiple levels to improve I/O performance. See [how JuiceFS store files](./architecture.md#how-juicefs-store-files). When processing write requests, JuiceFS first writes data into the client buffer as chunks / slices. Files are first split into one or more successive logical chunks (64MiB), chunks are isolated from each other, every chunk is then further split into one or more slices. A new slice will be created if it doesn't overlap nor adjoin any existing slices; otherwise, the affected existing slices will be updated. Slices are logical units for data persistency. When doing a flush, a slice will be split into one or more blocks (4MiB by default) and then uploaded to object storage, metadata will be updated when upload succeeds.

Evidently, only one ever growing slice and one final flush is needed when writing sequentially. This maximizes the write performance of the object storage. A simple [JuiceFS benchmark](../benchmark/performance_evaluation_guide.md) below shows sequentially writing a 1 GiB file with a 1 MiB I/O size at its first stage. The following figure shows the data flow in each component of the system.

![](../images/internals-write.png)

Use [`juicefs stats`](../reference/command_reference.md#stats) to obtain realtime performance monitoring metrics.

![](../images/internals-stats.png)

The first highlighted section in the above figure shows:

- The average size of I/O writing to the object storage is `object.put / object.put_c = 4 MiB`. It is the same as the default size of a block.
- The ratio of metadata transactions to object storage transactions is `meta.txn : object.put_c -= 1 : 16`. It means a single slice flush requires 1 update of metadata and 16 uploads to the object storage. It also shows that every flush operation transmits 4 MiB * 16 = 64 MiB of data, the same as the default size of a chunk.
- The average request size in the FUSE layer approximately equals to `fuse.write / fuse.ops ~= 128 KiB`, which is the same as the default limitation of the request size.

Generally, when writing a small file, the file will be uploaded to the object storage when it is being closed, and the I/O size is the same as the file size. The third stage in the above figure is creating 128 KiB small files, we can see that:

- The PUT size to the object storage is 128 KiB, calculated by `object.put / object.put_c`.
- The number of metadata transactions is two times to the number of PUT in approximate, since every file requires one create and one write.

When uploading objects smaller than block size, JuiceFS will try to write them simultaneously into the [local cache](../guide/cache_management.md) to improve future performance. From the third stage in above figure, we can see that when writing small files, the write bandwidth of the `blockcache` is the same as that of the object storage. Since the small files are cached, reading these small files is extremely fast (shown in the fourth stage).

A write operation will return immediately once it is committed to client buffer, resulting in a very low write latency (several microseconds in general). Uploading is triggered by internal events like the size or number of slices exceeding their limit, or when data is buffered for too long. Uploading can also be triggered by explicit calls such as closing a file or invoking `fsync`.

Client buffer will only be released after the data stored inside is uploaded. When faced with high write concurrency, if the buffer size isn't big enough (configured using [`--buffer-size`](../reference/command_reference.md#mount)), or the object storage's performance is simply not enough, file writes can block because the buffer cannot be released timely. Realtime buffer usage is shown in the `usage.buf` field in the metrics figure. JuiceFS Client will add a 10ms delay to every write to slow things down if the buffer usage exceeds the threshold. If the buffer usage is over twice the threshold, new writes will be completely suspended until the buffer is released. Therefore, if the write latency keeps increasing or the buffer usage has exceeded the threshold for a long while, you should increase `--buffer-size`. Also consider increasing the maximum number of upload concurrency ([`--max-uploads`](../reference/command_reference.md#mount), defaults to 20), which improves the upload bandwidth, thus boosting the buffer releasing.

### Random write {#random-write}

JuiceFS supports random writes, including mmap based random writes.

Know that block is an immutable object, this is due to the fact that most object storage services don't support edit in blocks, they can only re-upload and overwrite. Thus, when overwrite or random write happens, JuiceFS will never download the block, edit, and then re-upload since this cause serious IO amplifications, instead, the write is achieved on a new or existing slice, upload the relevant new blocks to the object storage, and then append the new slice to the slice list under the chunk. When file is read, what client sees is actually a consolidated view of all the slices.

Compared to sequential write, random write in a large file is significantly more complicated: there could be a number of intermittent slices in a chunk, possibly all smaller than 4 MiB, frequent random writes require frequent metadata updates, which in turn further impact performance. JuiceFS will schedule compaction tasks when the number of slices under a chunk exceeds limit, in order to improve read performance, you can also trigger compaction manually by running [`juicefs gc`](../administration/status_check_and_maintenance.md#gc).

### Client write cache {#client-write-cache}

Client write cache is also referred to as "Writeback mode" throughout the docs.

For scenarios that doesn't deem consistency and data security as top priority, enabling client write cache is also an option to further improve performance. When client write cache is enabled, flush operation will return immediately after writing data to the local cache directory. Local data will then be uploaded asynchronously to the object storage. In other words, the local cache directory is a cache layer for the object storage.

Learn more in [Client Write Cache](../guide/cache_management.md#writeback).

## Workflow of processing read {#workflow-of-read}

JuiceFS supports sequential read and random read (including mmap-based random read). When processing read requests, the object corresponding to the Block will be completely read through the `GetObject` API of the object storage, or only a certain range of data in the object may be read (e.g. the read range is limited by the `Range` parameter of [S3 API](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)). Meanwhile, prefetching will be performed (controlled by the [`--prefetch`](../reference/command_reference.md#mount) option) to download the complete data block into the local cache directory, as shown in the `blockcache` write speed in the above metrics figure, second stage. This is very good for sequential reads as all cached data will be utilized, maximizing the object storage access efficiency. The dataflow is illustrated in below figure:

![](../images/internals-read.png)

Although prefetching works well for sequential read, it might not be so effective for random read on large files, rather, it can cause read amplification and frequent cache eviction, consider disabling prefetching using `--prefetch=0`. It is always hard to design cache strategy for random read scenarios, one possible solution is to increase the cache size so that all data can be cached locally, while another solution could be disabling cache altogether (`--cache-size=0`), so that all reads are served directly by object storage, you'll be needing a high performance object storage service using this method.

Reading small files (smaller than block size) is much easier. The entire file can be read in a single request. Since small files are cached locally along the way when written to the object storage, future read will be very fast because they are already cached locally. For example, `juicefs bench` behaves precisely this way, so expect an impressive performance.
