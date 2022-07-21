---
sidebar_label: Data Processing Flow
sidebar_position: 3
slug: /internals/io_processing
---
# An introduction to the workflow of processing read and write

## Workflow of processing write

JuiceFS splits large files at multiple levels to improve I/O performance. See [how JuiceFS store files](../reference/how_juicefs_store_files.md). When processing write requests, JuiceFS first writes data into the client buffer and stores in chunks and slices. Chunks are successive logical units, which are split in a size of 64 MiB based on file offset. Chunks are isolated from each other and will be further split into slices if necessary. A new slice will be created if it doesn't overlap nor adjoin any existing slices; otherwise,  the affected existing slices will be updated. Slices are logical units for data persistency. When doing a flush, a slice will be first split into 4 MiB blocks and then uploaded to object storage, and eventually the metadata of the slice will be updated. As there is a one-to-one correspondence between blocks and objects, we only need **one** increasing slice and one final flush when writing sequentially. This maximizes the write performance of the object storage. A simple [JuiceFS benchmark](../benchmark/performance_evaluation_guide.md) below shows sequentially writing a 1 GiB file with a 1 MiB I/O size at its first stage. The following figure shows the data flow in each component of the system.

![write](../images/internals-write.png)

> **NOTICE**: Compression and encryption in this figure are disabled by default. To enable these features, please add option `--compress value` or `--encrypt-rsa-key value` to the formatting command.

To make it more intuitive, the following metrics figure shows the metrics from the `stats` command.

![stats](../images/internals-stats.png)

From the first highlighted section in the above figure, we can see,

- The average size of I/O writing to the object storage is `object.put / object.put_c = 4 MiB`. It is the same as the default size of a block.
- The ratio of metadata transactions to object storage transactions is `meta.txn : object.put_c -= 1 : 16`. It means a single slice flush requires 1 update of metadata and 16 uploads to the object storage. It also shows that every flush operation transmits 4 MiB * 16 = 64 MiB of data, the same as the default size of a chunk.
- The average request size in the FUSE layer approximately equals to `fuse.write / fuse.ops ~= 128 KiB`, which is the same as the default limitation of the request size.

Compared to sequential write, random write in a large file is more complicated. There could be **a number of intermittent** slices in a chunk, which makes it hard to crop the data into a size of 4 MiB, and lowers the performance since it requires multiple metadata updates.

Generally, when writing a small file, the file will be uploaded to the object storage when it is being closed, and the I/O size is the same as the file size. It could also be seen from the third highlighted section (creating 128 KiB small files) in the above metrics figure that,

- The PUT size to the object storage is 128 KiB, calculated by `object.put / object.put_c`.
- The number of metadata transactions is two times to the number of PUT in approximate, since every file requires one create and one write.

It's worth mentioning that, when uploading objects which are smaller than a block, JuiceFS will try to write them simultaneously into the local cache (specified by `--cache-dir`, both memory and disk are available) attempting to improve performance in the future. From the metrics figure above, we can see that when writing small files, the write bandwidth of the blockcache is the same as that of the object. Since the small files are cached, reading these small files is extremely fast (as shown in the fourth highlighted section).

JuiceFS has very low write latency, about several microseconds in general. It is because a write operation will return immediately once it is done writing to the memory buffer of the client. It will not actually upload to the object storage until the upload action is triggered by internal events or by external applications. The internal events like the exceeding of the slice size, the slice number or the buffering time will trigger the upload action automatically, and the external application events such as closing a file or invoking `fsync` could also trigger the upload action. Since the buffer capacity will only be released after the data stored in the buffer is persisted, we may encounter a write block if the object storage is not fast enough to handle large-scale concurrencies to keep the buffer being released timely. Specifically, the default buffer size is 300 MiB, which can be set by the mount option `--buffer-size`. The usage of the buffer in realtime is shown in the `usage.buf` field in the metrics figure. JuiceFS client will prepend a 10ms delay to every write operation to slow down the operation if the buffer usage exceeds the threshold. New writes will be suspended until the buffer is released, if the usage of the buffer is over twice the threshold. Therefore, if the write latency keeps increasing or the buffer usage has exceeded the threshold for a long while, a larger `--buffer-size` may be needed. In addition, increasing `--max-uploads`, which is the maximum number of concurrencies that defaults to 20, could also be helpful to improve the bandwidth for writing to the object storage, thus boosting the buffer releasing.

### Writeback mode

Enabling `--writeback` when mounting is also an option to further improve the performance if the data consistency and reliability are not crucial. If the writeback mode is enabled, slice flush operation will return immediately after writing data to the local staging directory shared with cache. The data will then be uploaded asynchronously to the object storage by a backend thread. It's worth mentioning that, different from the so-called _write to memory first_ mechanism, the writeback mode of JuiceFS writes data into a local cache directory (details vary with the hardware and the local file systems used for the cache directory in practice). In other words, the local directory is a cache layer of the object storage.

With the writeback mode enabled, JuiceFS will aggressively preserve all of the data into the cache directory without checking the size of the objects to be uploaded by default. This is useful in some scenarios which generate a large number of intermediate files such as compiling software. In addition, a new parameter `--upload-delay` has been introduced since JuiceFS v0.17, which is for delaying the upload action and caching the data locally in a more aggressive way. If a file has been deleted before being uploaded, the upload action will ignore this file. This can not only improve performance but also save cost. Meanwhile, compared with a local disk, JuiceFS uploads files automatically when the cache directory is running out of space, which keeps the applications away from unexcepted failures. This is useful for applications requiring temporary file storage such as Spark shuffle.

## Workflow of processing read

JuiceFS reads objects from object storage block by block in a size of 4 MiB, which could be considered as a kind of read-ahead. Data read will be saved into the local cache directory for future use, proven by the high write bandwidth of blockcache shown in the 2nd highlighted section in the above metrics figure. Obviously, all the cached data will be accessed by subsequent reads, and the high cache hit rate can maximize the object storage read/write performance. The dataflow along each component of this case shows as follows:

![read](../images/internals-read.png)

> **NOTICE**: When a read object arrives, if the decryption and compression are enabled, it will be first decrypted and then uncompressed by JuiceFS client, which is contrary to write.

This strategy is not effective for randomly reading a small block within a large file. In this case, the system utilization will be lower due to the read amplification and the frequent write and evict to the local cache. Unfortunately, it is hard to gain a high benefit from a cache system in this case. One possible solution is to enlarge the total capacity of the cache system to cache as much data as possible, while another solution could be disabling cache by setting `--cache-size 0` and using an object storage with a performance as high as possible.

Reading small files is much easier. The whole file is often read in a single request. Since small files are cached first before writing to an object storage, if a file is read soon after it is written, the read operation will hit the local cache, like what JuiceFS bench does, and an impressive performance can be expected.
