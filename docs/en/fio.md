# fio Benchmark

## Testing Approach

Performed a sequential read/write benchmark on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [fio](https://github.com/axboe/fio).

## Testing Tool

The following tests were performed by fio 3.1.

Sequential read test (numjobs: 1):

```
fio --name=sequential-read --directory=/s3fs --rw=read --refill_buffers --bs=4M --size=4G
fio --name=sequential-read --directory=/efs --rw=read --refill_buffers --bs=4M --size=4G
fio --name=sequential-read --directory=/jfs --rw=read --refill_buffers --bs=4M --size=4G
```

Sequential write test (numjobs: 1):

```
fio --name=sequential-write --directory=/s3fs --rw=write --refill_buffers --bs=4M --size=4G --end_fsync=1
fio --name=sequential-write --directory=/efs --rw=write  --refill_buffers --bs=4M --size=4G --end_fsync=1
fio --name=sequential-write --directory=/jfs --rw=write --refill_buffers --bs=4M --size=4G --end_fsync=1
```

Sequential read test (numjobs: 16):

```
fio --name=big-file-multi-read --directory=/s3fs --rw=read --refill_buffers --bs=4M --size=4G --numjobs=16
fio --name=big-file-multi-read --directory=/efs --rw=read --refill_buffers --bs=4M --size=4G --numjobs=16
fio --name=big-file-multi-read --directory=/jfs --rw=read --refill_buffers --bs=4M --size=4G --numjobs=16
```

Sequential write test (numjobs: 16):

```
fio --name=big-file-multi-write --directory=/s3fs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=16 --end_fsync=1
fio --name=big-file-multi-write --directory=/efs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=16 --end_fsync=1
fio --name=big-file-multi-write --directory=/jfs --rw=write --refill_buffers --bs=4M --size=4G --numjobs=16 --end_fsync=1
```

## Testing Environment

In the following test results, all fio tests based on the c5d.18xlarge EC2 instance (72 CPU, 144G RAM), Ubuntu 18.04 LTS (Kernel 5.4.0) system, JuiceFS use the local Redis instance (version 4.0.9) to store metadata.

JuiceFS mount command:

```
./juicefs format --storage=s3 --bucket=https://<BUCKET>.s3.<REGION>.amazonaws.com localhost benchmark
./juicefs mount --max-uploads=150 --io-retries=20 localhost /jfs
```

EFS mount command (the same as the configuration page):

```
mount -t nfs -o nfsvers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,noresvport, <EFS-ID>.efs.<REGION>.amazonaws.com:/ /efs
```

S3FS (version 1.82) mount command:

```
s3fs <BUCKET>:/s3fs /s3fs -o host=https://s3.<REGION>.amazonaws.com,endpoint=<REGION>,passwd_file=${HOME}/.passwd-s3fs
```

## Testing Result

![Sequential Read Write Benchmark](../images/sequential-read-write-benchmark.svg)
