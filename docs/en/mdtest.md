# mdtest Benchmark

## Testing Approach

Performed a metadata test on JuiceFS, [EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) by [mdtest](https://github.com/hpc/ior).

## Testing Tool

The following tests were performed by mdtest 3.4.
Arguments of mdtest are adjusted to ensure the command can be finished in 5 minutes.

```
./mdtest -d /s3fs/mdtest -b 6 -I 8 -z 2
./mdtest -d /efs/mdtest -b 6 -I 8 -z 4
./mdtest -d /jfs/mdtest -b 6 -I 8 -z 4
```

## Testing Environment

In the following test results, all mdtest tests based on the c5.large EC2 instance (2 CPU, 4G RAM), Ubuntu 18.04 LTS (Kernel 5.4.0) system, JuiceFS use Redis (version 4.0.9) running on a c5.large EC2 instance in the same available zone to store metadata.

JuiceFS mount command:

```
./juicefs format --storage=s3 --bucket=https://<BUCKET>.s3.<REGION>.amazonaws.com localhost benchmark
nohup ./juicefs mount localhost /jfs &
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

![Metadata Benchmark](../images/metadata-benchmark.svg)

### S3FS
```
mdtest-3.4.0+dev was launched with 1 total task(s) on 1 node(s)
Command line used: ./mdtest '-d' '/s3fs/mdtest' '-b' '6' '-I' '8' '-z' '2'
WARNING: Read bytes is 0, thus, a read test will actually just open/close.
Path                : /s3fs/mdtest
FS                  : 256.0 TiB   Used FS: 0.0%   Inodes: 0.0 Mi   Used Inodes: -nan%
Nodemap: 1
1 tasks, 344 files/directories

SUMMARY rate: (of 1 iterations)
   Operation                      Max            Min           Mean        Std Dev
   ---------                      ---            ---           ----        -------
   Directory creation        :          5.977          5.977          5.977          0.000
   Directory stat            :        435.898        435.898        435.898          0.000
   Directory removal         :          8.969          8.969          8.969          0.000
   File creation             :          5.696          5.696          5.696          0.000
   File stat                 :         68.692         68.692         68.692          0.000
   File read                 :         33.931         33.931         33.931          0.000
   File removal              :         23.658         23.658         23.658          0.000
   Tree creation             :          5.951          5.951          5.951          0.000
   Tree removal              :          9.889          9.889          9.889          0.000
```

### EFS

```
mdtest-3.4.0+dev was launched with 1 total task(s) on 1 node(s)
Command line used: ./mdtest '-d' '/efs/mdtest' '-b' '6' '-I' '8' '-z' '4'
WARNING: Read bytes is 0, thus, a read test will actually just open/close.
Path                : /efs/mdtest
FS                  : 8388608.0 TiB   Used FS: 0.0%   Inodes: 0.0 Mi   Used Inodes: -nan%
Nodemap: 1
1 tasks, 12440 files/directories

SUMMARY rate: (of 1 iterations)
   Operation                      Max            Min           Mean        Std Dev
   ---------                      ---            ---           ----        -------
   Directory creation        :        192.301        192.301        192.301          0.000
   Directory stat            :       1311.166       1311.166       1311.166          0.000
   Directory removal         :        213.132        213.132        213.132          0.000
   File creation             :        179.293        179.293        179.293          0.000
   File stat                 :        915.230        915.230        915.230          0.000
   File read                 :        371.012        371.012        371.012          0.000
   File removal              :        217.498        217.498        217.498          0.000
   Tree creation             :        187.906        187.906        187.906          0.000
   Tree removal              :        218.357        218.357        218.357          0.000
```

### JuiceFS

```
mdtest-3.4.0+dev was launched with 1 total task(s) on 1 node(s)
Command line used: ./mdtest '-d' '/jfs/mdtest' '-b' '6' '-I' '8' '-z' '4'
WARNING: Read bytes is 0, thus, a read test will actually just open/close.
Path                : /jfs/mdtest
FS                  : 1024.0 TiB   Used FS: 0.0%   Inodes: 10.0 Mi   Used Inodes: 0.0%
Nodemap: 1
1 tasks, 12440 files/directories

SUMMARY rate: (of 1 iterations)
   Operation                      Max            Min           Mean        Std Dev
   ---------                      ---            ---           ----        -------
   Directory creation        :       1416.582       1416.582       1416.582          0.000
   Directory stat            :       3810.083       3810.083       3810.083          0.000
   Directory removal         :       1115.108       1115.108       1115.108          0.000
   File creation             :       1410.288       1410.288       1410.288          0.000
   File stat                 :       5023.227       5023.227       5023.227          0.000
   File read                 :       3487.947       3487.947       3487.947          0.000
   File removal              :       1163.371       1163.371       1163.371          0.000
   Tree creation             :       1503.004       1503.004       1503.004          0.000
   Tree removal              :       1119.806       1119.806       1119.806          0.000
```
