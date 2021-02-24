# FAQ

## Why doesn't JuiceFS support XXX object storage?

JuiceFS already supported many object storage, please check [the list](#supported-object-storage) first. If this object storage is compatible with S3, you could treat it as S3. Otherwise, try reporting issue.

## Can I use Redis cluster?

The simple answer is no. JuiceFS uses [transaction](https://redis.io/topics/transactions) to guarantee the atomicity of metadata operations, which is not well supported in cluster mode. Sentinal or other HA solution for Redis are needed.

## What's the difference between JuiceFS and XXX?

See ["Comparison with Others"](docs/en/comparison_with_others.md) for more information.

## How is the performance of JuiceFS?

JuiceFS is a distributed file system, the latency of metedata is determined by 1 (reading) or 2 (writing) round trip(s) between client and metadata service (usually 1-3 ms). The latency of first byte is determined by the performance of underlying object storage (usually 20-100 ms). Throughput of sequential read/write could be 50MB/s - 2800MiB/s (see [fio benchmark](fio.md) for more information), depends on network bandwidth and how the data could be compressed.

JuiceFS is built with multiple layers of caching (invalidated automatically), once the caching is warmed up, the latency and throughput of JuiceFS could be close to local filesystem (having the overhead of FUSE).

## Does JuiceFS support random read/write?

Yes, including those issued using mmap. Currently JuiceFS is optimized for sequential reading/writing, and optimized for random reading/writing is work in progress.

## When my update will be visible to other clients?

All the metadata updates are immediately visible to all others. The new data written by `write()` will be buffered in kernel or client, visible to other processes on the same machine, not visible to other machines.

After a certain time, call `fdatasync()` or `close()` to force upload the data to the object storage and update the metadata, other clients can visit the updates. It is also the strategy adopted by the vast majority of distributed file systems.

See ["Write Cache in Client"](cache_management.md#write-cache-in-client) for more information.

## How to copy a large number of small files into JuiceFS quickly?

You could mount JuiceFS with [`--writeback` option](command_reference.md#juicefs-mount), which will write the small files into local disks first, then upload them to object storage in background, this could speedup coping many small files into JuiceFS.

See ["Write Cache in Client"](cache_management.md#write-cache-in-client) for more information.

## Can I mount JuiceFS without `root`?

Yes, JuiceFS could be mounted using `juicefs` without root. The default directory for caching is `$HOME/.juicefs/cache` (macOS) or `/var/jfsCache` (Linux), you should change that to a directory which you have write permission.

See ["Read Cache in Client"](cache_management.md#read-cache-in-client) for more information.

## How to unmount JuiceFS?

Use [`juicefs umount`](command_reference.md#juicefs-umount) command to unmount the volume.

## How to upgrade JuiceFS client?

First unmount JuiceFS volume, then re-mount the volume with newer version client.
