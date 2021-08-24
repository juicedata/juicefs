# fio 基准测试

## 测试方法

使用 [fio](https://github.com/axboe/fio) 在 JuiceFS、[EFS](https://aws.amazon.com/efs) 和 [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) 上执行顺序读、顺序写基准测试。

## 测试工具

以下测试使用的工具为 fio 3.1。

顺序读测试 (任务数: 1):

```
fio --name=sequential-read --directory=/s3fs --rw=read --refill_buffers --bs=4M --size=4G
fio --name=sequential-read --directory=/efs --rw=read --refill_buffers --bs=4M --size=4G
fio --name=sequential-read --directory=/jfs --rw=read --refill_buffers --bs=4M --size=4G
```

顺序写测试 (任务数: 1):

```
fio --name=sequential-write --directory=/s3fs --rw=write --refill_buffers --bs=4M --size=4G --end_fsync=1
fio --name=sequential-write --directory=/efs --rw=write  --refill_buffers --bs=4M --size=4G --end_fsync=1
fio --name=sequential-write --directory=/jfs --rw=write --refill_buffers --bs=4M --size=4G --end_fsync=1
```

顺序读测试 (任务数: 16):

```
fio --name=big-file-multi-read --directory=/s3fs --rw=read --refill_buffers --bs=4M --size=4G --numjobs=16
fio --name=big-file-multi-read --directory=/efs --rw=read --refill_buffers --bs=4M --size=4G --numjobs=16
fio --name=big-file-multi-read --directory=/jfs --rw=read --refill_buffers --bs=4M --size=4G --numjobs=16
```

顺序写测试 (任务数: 16):

```
fio --name=big-file-multi-write --directory=/s3fs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=16 --end_fsync=1
fio --name=big-file-multi-write --directory=/efs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=16 --end_fsync=1
fio --name=big-file-multi-write --directory=/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=16 --end_fsync=1
```

## 测试环境

以下测试结果均使用 fio 在亚马逊云 c5d.18xlarge EC2  (72 CPU, 144G RAM) 实例得出，操作系统采用 Ubuntu 18.04 LTS (Kernel 5.4.0) ，JuiceFS 使用同主机的本地 Redis (version 4.0.9) 实例存储元数据。

JuiceFS 挂载命令:

```
./juicefs format --storage=s3 --bucket=https://<BUCKET>.s3.<REGION>.amazonaws.com localhost benchmark
./juicefs mount --max-uploads=150 --io-retries=20 localhost /jfs
```

EFS 挂载命令 (与配置说明中一致):

```
mount -t nfs -o nfsvers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,noresvport, <EFS-ID>.efs.<REGION>.amazonaws.com:/ /efs
```

S3FS (version 1.82) 挂载命令:

```
s3fs <BUCKET>:/s3fs /s3fs -o host=https://s3.<REGION>.amazonaws.com,endpoint=<REGION>,passwd_file=${HOME}/.passwd-s3fs
```

## 测试结果

![Sequential Read Write Benchmark](../images/sequential-read-write-benchmark.svg)
