# JuiceFS 单机性能测试

## 测试方法

使用 [fio](https://github.com/axboe/fio) 对 JuiceFS 进行的单机性能测试。

### 测试工具

使用 fio 3.1 完成以下测试。

Fio 有很多参数可以影响测试结果，在以下测试中我们遵循的原则是：尽量采用各项参数的默认值，不针对系统、硬件等进行调优，以此提供一个性能参考。下面是使用到的参数列表：

- `--name`

  测试任务名称。

- `--directory`

  测试数据读写路径，即 JuiceFS 挂载点，以下测试以 `/jfs` 举例。

- `--rw`

  读写模式，以下测试会涉及 read（顺序读），write（顺序写）。

- `--bs`

  每次读写的块数据大小。

- `--size`

  这次测试任务的总文件大小。

- `--filesize`

  测试任务中生成的单个文件大小，在小文件顺序读写测试中用到。

- `--numjobs`

  并发任务数，fio 默认使用多进程（process）方式。

- `--nrfiles`

  在每个任务中生成的文件数量。

- `--refill_buffers`

  默认情况下，fio 会在测试任务开始时创建用于生成测试文件的数据片段，并一直重用这些数据。使用这个参数后，会在每次 I/O 提交后重新生成数据，保证生成测试文件内容有充分的随机性。

- `--file_service_type`

  用来定义测试任务中的文件选取方式，有 random, roundrobin, sequential 三种。在小文件顺序读写测试中使用了这个参数来保证 fio 是一个接一个的读写文件，没有并行的文件操作。

因为 JuiceFS 在写数据到对象存储时，默认使用 ZStandard 进行压缩，所以在下面各个测试任务中都加了 `--refill_buffers` 参数，让 fio 生成的数据尽可能随机且没有规律，使得生成数据的压缩比很低，效果尽可能接近于实际业务场景中性能较差的情况。换言之，在实际业务中，JuiceFS 的表现大多好于本测试的性能指标。

### 测试环境

基于不同的云服务商、对象存储、虚拟机类型、操作系统等差异，性能表现都会存在差异。

以下测试结果中 JuiceFS 文件系统是基于 AWS us-west2 区的 S3 创建（创建方法请查看 [上手指南](https://juicefs.com/docs/zh/getting_started.html#create-filesystem)），全部 fio 测试在 c5d.18xlarge EC2 实例（72 CPU，144G RAM），Ubuntu 18.04 LTS (Kernel 4.15.0)系统。

选择 c5d.18xlarge 机型因为它有 25Gbit 网络，同时可以启用 Elastic Network Adapter（ENA），可以 [将 EC2 到 S3 的通信提升至 25Gbps](https://aws.amazon.com/cn/blogs/china/the-floodgates-are-open-increased-network-bandwidth-for-ec2-instances/)，保证测试中 JuiceFS 不会受网络带宽限制。

以下测试如没有特殊说明，均使用默认配置挂载 JuiceFS（挂载方法请查看 [上手指南](https://juicefs.com/docs/zh/getting_started.html#mount-filesystem)）。

## 大文件顺序读写测试

在日志收集、数据备份、大数据分析等很多场景中，都需要做大文件顺序读写，这也是 JuiceFS 适用的典型场景。

JucieFS 页大小的设置是顺序读写吞吐能力的主要影响因素，页大小越大，顺序读写吞吐能力越强，见下面的测试结果。

注意：此处需要事先创建不同页大小的 JuiceFS 文件系统并进行挂载，在测试脚本中对 `--directory` 参数换成对应的 JuiceFS 挂载点。

### 大文件顺序读

![_images/big-file-seq-read-2019.png](https://juicefs.com/docs/zh/_images/big-file-seq-read-2019.png)

```
fio --name=big-file-sequential-read \
    --directory=/jfs \
    --rw=read --refill_buffers \
    --bs=256k --size=4G
```

### 大文件顺序写

![_images/big-file-seq-write-2019.png](https://juicefs.com/docs/zh/_images/big-file-seq-write-2019.png)

```
fio --name=big-file-sequential-write \
    --directory=/jfs \
    --rw=write --refill_buffers \
    --bs=256k --size=4G
```

### 大文件并发读

![_images/big-file-multi-read-2019.png](https://juicefs.com/docs/zh/_images/big-file-multi-read-2019.png)

```
fio --name=big-file-multi-read \
--directory=/jfs \
--rw=read --refill_buffers \
--bs=256k --size=4G \
--numjobs={1, 2, 4, 8, 16}
```

### 大文件并发写

![_images/big-file-multi-write-2019.png](https://juicefs.com/docs/zh/_images/big-file-multi-write-2019.png)

```
fio --name=big-file-multi-write \
    --directory=/jfs \
    --rw=write --refill_buffers \
    --bs=256k --size=4G  \
    --numjobs={1, 2, 4, 8, 16}
```

### 大文件随机读

![_images/big-file-rand-read-2019.png](https://juicefs.com/docs/zh/_images/big-file-rand-read-2019.png)

```
fio --name=big-file-rand-read \
    --directory=/jfs \
    --rw=randread --refill_buffers \
    --size=4G --filename=randread.bin \
    --bs={4k, 16k, 64k, 256k} --pre_read=1

sync && echo 3 > /proc/sys/vm/drop_caches

fio --name=big-file-rand-read \
    --directory=/jfs \
    --rw=randread --refill_buffers \
    --size=4G --filename=randread.bin \
    --bs={4k, 16k, 64k, 256k}
```

为了精准地测试大文件随机读的性能，在这里我们先使用 `fio` 将文件预读取一遍，然后清除内核缓存（包括 PageCache, dentries, inodes 缓存），接着使用 `fio` 进行随机读的性能测试。

在大文件随机读的场景，为了获得更好的性能，建议将挂载参数的缓存大小设置为大于将要读取的文件大小。

### 大文件随机写

![_images/big-file-rand-write-2019.png](https://juicefs.com/docs/zh/_images/big-file-rand-write-2019.png)

```
fio --name=big-file-random-write \
    --directory=/jfs \
    --rw=randwrite --refill_buffers \
    --size=4G --bs={4k, 16k, 64k, 256k}
```

## 小文件读写测试

### 小文件读

JuiceFS 在挂载时默认开启 1G 本地数据缓存，缓存能大幅提升小文件读取的 IOPS。可以在挂载 JuiceFS 时加上 `--cache-size=0` 的参数关闭缓存，下面做了有无缓存时的性能对比。

![_images/small-file-seq-read-2019.png](https://juicefs.com/docs/zh/_images/small-file-seq-read-2019.png)

```
fio --name=small-file-seq-read \
    --directory=/jfs \
    --rw=read --file_service_type=sequential \
    --bs={file_size} --filesize={file_size} --nrfiles=1000
```

### 小文件写

在 JuiceFS 挂载时加上 `--writeback` 参数客户端写缓存（详细说明可以查看 [缓存](https://juicefs.com/docs/zh/cache.html#client-write-cache) 一章），可以对小文件顺序写性能大幅提升，下面做了对比测试。

在默认的 fio 测试行为中会把文件的关闭操作留在任务最后执行，这样在分布式文件系统中存在因网络异常等因素丢失数据的可能。所以我们在 fio 的测试参数中使用了 `--file_service_type=sequential` 参数，这样 fio 会在测试任务中保证写完一个文件（执行 flush & close，把数据全部写入对象存储）再进行下一个文件。

![_images/small-file-seq-write-2019.png](https://juicefs.com/docs/zh/_images/small-file-seq-write-2019.png)

```
fio --name=small-file-seq-read \
    --directory=/jfs \
    --rw=write --file_service_type=sequential \
    --bs={file_size} --filesize={file_size} --nrfiles=1000
```

### 小文件并发读

![_images/small-file-multi-read-2019.png](https://juicefs.com/docs/zh/_images/small-file-multi-read-2019.png)

```
fio --name=small-file-multi-read \
    --directory=/jfs \
    --rw=read --file_service_type=sequential \
    --bs=4k --filesize=4k --nrfiles=1000 \
    --numjobs={1, 2, 4, 8, 16}
```

### 小文件并发写

![_images/small-file-multi-write-2019.png](https://juicefs.com/docs/zh/_images/small-file-multi-write-2019.png)

```
fio --name=small-file-multi-write \
    --directory=/jfs \
    --rw=write --file_service_type=sequential \
    --bs=4k --filesize=4k --nrfiles=1000 \
    --numjobs={1, 2, 4, 8, 16}
```

Fio 测试任务使用的进程（process）方式，性能与并发进程数线性相关，随进程数增加，有少量衰减。