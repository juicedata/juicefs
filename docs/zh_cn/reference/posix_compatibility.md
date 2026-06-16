---
title: POSIX 兼容性
sidebar_position: 6
slug: /posix_compatibility
---

JuiceFS 借助于 pjdfstest 和 LTP 来验证其对 POSIX 的兼容性。

## Pjdfstest

[Pjdfstest](https://github.com/pjd/pjdfstest) 是一个用来帮助验证 POSIX 系统调用的测试集，JuiceFS 通过了其最新的 8789 项测试：

```
All tests successful.

Test Summary Report
-------------------
/root/code/juicefs/posix_test/pjdfstest/tests/chown/00.t          (Wstat: 0 Tests: 1280 Failed: 0)
  TODO passed:   1054, 1058, 1064, 1069, 1073, 1079, 1084
                1088, 1094, 1099, 1103, 1109, 1114, 1118
                1124, 1129, 1133, 1139, 1144
Files=237, Tests=8789, 191 wallclock secs ( 1.61 usr  0.40 sys + 25.44 cusr 36.67 csys = 64.12 CPU)
Result: PASS
```

:::note 注意
测试 pjdfstest 时，需要将 JuiceFS 的回收站关闭，因为 pjdfstest 测试的删除行为是直接删除而非进入回收站，而 JuiceFS 回收站是默认开启的。
关闭回收站命令：`juicefs config <meta-url> --trash-days 0`
:::

此外，JuiceFS 还提供：

- 关闭再打开（close-to-open）一致性。一旦一个文件写入完成并关闭，之后的打开和读操作保证可以访问之前写入的数据。如果是在同一个挂载点，所有写入的数据都可以立即读。
- 重命名以及所有其他元数据操作都是原子的，由元数据引擎的事务机制保证。
- 当文件被删除后，同一个挂载点上如果已经打开了，文件还可以继续访问。
- 支持 mmap
- 支持 fallocate 以及空洞
- 支持扩展属性
- 支持 BSD 锁（flock）
- 支持传统 POSIX 记录锁（fcntl）

:::note 注意
POSIX 记录锁分为**传统锁**和 **OFD 锁**（Open file description locks）两类，它们的加锁操作命令分别为 `F_SETLK` 和 `F_OFD_SETLK`。受限于 FUSE 内核模块的实现，目前 JuiceFS 只支持传统类型的记录锁。更多细节可参见：[https://man7.org/linux/man-pages/man2/fcntl.2.html](https://man7.org/linux/man-pages/man2/fcntl.2.html)。
:::

## LTP

[LTP](https://github.com/linux-test-project/ltp)（Linux Test Project）是一个由 IBM，Cisco 等多家公司联合开发维护的项目，旨在为开源社区提供一个验证 Linux 可靠性和稳定性的测试集。LTP 中包含了各种工具来检验 Linux 内核和相关特性；JuiceFS 通过了其中与文件系统相关的大部分测试例。

### 测试环境

- 测试主机：阿里云 ESC 服务器
- 操作系统：Ubuntu 24.04 (Kernel `6.8.0-55-generic`)
- JuiceFS 版本：1.4.0-beta2
- LTP 版本：20260529

### 测试步骤

1. 在 GitHub 下载 LTP [源码包](https://github.com/linux-test-project/ltp/releases/download/20260529/ltp-full-20260529.tar.xz)

2. 解压后编译安装：

   ```bash
   tar -xvf ltp-full-20260529.tar.xz
   cd ltp-full-20260529
   ./configure
   make all
   make install
   ```

3. 安装 kirk（新版 LTP 使用 kirk 替代了已移除的 runltp）：

   ```bash
   pip install kirk
   ```

4. 测试工具安装在 `/opt/ltp`，需先切换到此目录：

   ```bash
   cd /opt/ltp
   ```

5. 创建测试配置文件。为方便测试，从 `runtest/fs` 和 `runtest/syscalls` 中删去压力测试、与文件系统不相关的条目以及不在 JuiceFS 上运行的测试，修改后保存到文件 `fs-jfs` 和 `syscalls-jfs`（具体删除列表见[附录](#附录)）：

6. 执行测试：

   ```bash
   export LTPROOT=/opt/ltp
   export PATH=/opt/ltp/testcases/bin:$PATH
   cd /mnt/jfs
   kirk --run-suite fs_bind --sut default --tmp-dir /mnt/jfs
   kirk --run-suite fs_perms_simple --sut default --tmp-dir /mnt/jfs
   kirk --run-suite smoketest --sut default --tmp-dir /mnt/jfs
   kirk --run-suite fs-jfs --sut default --tmp-dir /mnt/jfs
   kirk --run-suite syscalls-jfs --sut default --tmp-dir /mnt/jfs
   kirk --run-suite fcntl-locktests --sut default --tmp-dir /mnt/jfs
   ```

### 测试结果

| 测试套件 | 运行数 | 通过 | 失败 | Broken | 跳过 |
|---------|--------|------|------|--------|------|
| fs_bind | 95 | 95 | 0 | 0 | 0 |
| fs_perms_simple | 18 | 18 | 0 | 0 | 0 |
| smoketest | 12 | 10 | 0 | 0 | 2 |
| fs-jfs | 30 | 30 | 0 | 0 | 0 |
| syscalls-jfs | 1323 | 1300 | 0 | 0 | 23 |
| fcntl-locktests | 1 | 1 | 0 | 0 | 0 |
| **合计** | **1479** | **1454** | **0** | **0** | **25** |

在 JuiceFS 上运行的测试中，与文件系统相关的测试全部通过，0 失败，0 Broken。

### 附录

在 `fs` 和 `syscalls` 文件中删去的测试例：

```bash
# fs --> fs-jfs
gf01 growfiles -W gf01 -b -e 1 -u -i 0 -L 20 -w -C 1 -l -I r -T 10 -f glseek20 -S 2 -d $TMPDIR
gf02 growfiles -W gf02 -b -e 1 -L 10 -i 100 -I p -S 2 -u -f gf03_ -d $TMPDIR
gf03 growfiles -W gf03 -b -e 1 -g 1 -i 1 -S 150 -u -f gf05_ -d $TMPDIR
gf04 growfiles -W gf04 -b -e 1 -g 4090 -i 500 -t 39000 -u -f gf06_ -d $TMPDIR
gf05 growfiles -W gf05 -b -e 1 -g 5000 -i 500 -t 49900 -T10 -c9 -I p -u -f gf07_ -d $TMPDIR
gf06 growfiles -W gf06 -b -e 1 -u -r 1-5000 -R 0--1 -i 0 -L 30 -C 1 -f g_rand10 -S 2 -d $TMPDIR
gf07 growfiles -W gf07 -b -e 1 -u -r 1-5000 -R 0--2 -i 0 -L 30 -C 1 -I p -f g_rand13 -S 2 -d $TMPDIR
gf08 growfiles -W gf08 -b -e 1 -u -r 1-5000 -R 0--2 -i 0 -L 30 -C 1 -f g_rand11 -S 2 -d $TMPDIR
gf09 growfiles -W gf09 -b -e 1 -u -r 1-5000 -R 0--1 -i 0 -L 30 -C 1 -I p -f g_rand12 -S 2 -d $TMPDIR
gf10 growfiles -W gf10 -b -e 1 -u -r 1-5000 -i 0 -L 30 -C 1 -I l -f g_lio14 -S 2 -d $TMPDIR
gf11 growfiles -W gf11 -b -e 1 -u -r 1-5000 -i 0 -L 30 -C 1 -I L -f g_lio15 -S 2 -d $TMPDIR
gf12 mkfifo $TMPDIR/gffifo17; growfiles -b -W gf12 -e 1 -u -i 0 -L 30 $TMPDIR/gffifo17
gf13 mkfifo $TMPDIR/gffifo18; growfiles -b -W gf13 -e 1 -u -i 0 -L 30 -I r -r 1-4096 $TMPDIR/gffifo18
gf14 growfiles -W gf14 -b -e 1 -u -i 0 -L 20 -w -l -C 1 -T 10 -f glseek19 -S 2 -d $TMPDIR
gf15 growfiles -W gf15 -b -e 1 -u -r 1-49600 -I r -u -i 0 -L 120 -f Lgfile1 -d $TMPDIR
gf16 growfiles -W gf16 -b -e 1 -i 0 -L 120 -u -g 4090 -T 101 -t 408990 -l -C 10 -c 1000 -S 10 -f Lgf02_ -d $TMPDIR
gf17 growfiles -W gf17 -b -e 1 -i 0 -L 120 -u -g 5000 -T 101 -t 499990 -l -C 10 -c 1000 -S 10 -f Lgf03_ -d $TMPDIR
gf18 growfiles -W gf18 -b -e 1 -i 0 -L 120 -w -u -r 10-5000 -I r -l -S 2 -f Lgf04_ -d $TMPDIR
gf19 growfiles -W gf19 -b -e 1 -g 5000 -i 500 -t 49900 -T10 -c9 -I p -o O_RDWR,O_CREAT,O_TRUNC -u -f gf08i_ -d $TMPDIR
gf20 growfiles -W gf20 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -r 1-256000:512 -R 512-256000 -T 4 -f gfbigio-$$ -d $TMPDIR
gf21 growfiles -W gf21 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -g 20480 -T 10 -t 20480 -f gf-bld-$$ -d $TMPDIR
gf22 growfiles -W gf22 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -g 20480 -T 10 -t 20480 -f gf-bldf-$$ -d $TMPDIR
gf23 growfiles -W gf23 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -r 512-64000:1024 -R 1-384000 -T 4 -f gf-inf-$$ -d $TMPDIR
gf24 growfiles -W gf24 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -g 20480 -f gf-jbld-$$ -d $TMPDIR
gf25 growfiles -W gf25 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -r 1024000-2048000:2048 -R 4095-2048000 -T 1 -f gf-large-gs-$$ -d $TMPDIR
gf26 growfiles -W gf26 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -r 128-32768:128 -R 512-64000 -T 4 -f gfsmallio-$$ -d $TMPDIR
gf27 growfiles -W gf27 -b -D 0 -w -g 8b -C 1 -b -i 1000 -u -f gfsparse-1-$$ -d $TMPDIR
gf28 growfiles -W gf28 -b -D 0 -w -g 16b -C 1 -b -i 1000 -u -f gfsparse-2-$$ -d $TMPDIR
gf29 growfiles -W gf29 -b -D 0 -r 1-4096 -R 0-33554432 -i 0 -L 60 -C 1 -u -f gfsparse-3-$$ -d $TMPDIR
gf30 growfiles -W gf30 -D 0 -b -i 0 -L 60 -u -B 1000b -e 1 -o O_RDWR,O_CREAT,O_SYNC -g 20480 -T 10 -t 20480 -f gf-sync-$$ -d $TMPDIR
rwtest01 export LTPROOT; rwtest -N rwtest01 -c -q -i 60s  -f sync 10%25000:$TMPDIR/rw-sync-$$
rwtest02 export LTPROOT; rwtest -N rwtest02 -c -q -i 60s  -f buffered 10%25000:$TMPDIR/rw-buffered-$$
rwtest03 export LTPROOT; rwtest -N rwtest03 -c -q -i 60s -n 2  -f buffered -s mmread,mmwrite -m random -Dv 10%25000:$TMPDIR/mm-buff-$$
rwtest04 export LTPROOT; rwtest -N rwtest04 -c -q -i 60s -n 2  -f sync -s mmread,mmwrite -m random -Dv 10%25000:$TMPDIR/mm-sync-$$
rwtest05 export LTPROOT; rwtest -N rwtest05 -c -q -i 50 -T 64b 500b:$TMPDIR/rwtest01%f
iogen01 export LTPROOT; rwtest -N iogen01 -i 120s -s read,write -Da -Dv -n 2 500b:$TMPDIR/doio.f1.$$ 1000b:$TMPDIR/doio.f2.$$
quota_remount_test01 quota_remount_test01.sh
isofs isofs.sh

## syscalls --> syscalls-jfs
# syscall中删除的用例较多，这里仅记录一些fuse和juicefs不支持的用例
# 完整列表请参考 .github/workflows/bash/rm_syscalls

# 纯内核功能测试，与用户态文件系统无关
close_range01 close_range01
close_range02 close_range02
openat201 openat201
openat202 openat202
openat203 openat203

# 测试特定CVE（SELINUX），与juicefs无关
listxattr04 listxattr04
io_uring02 io_uring02

# 因为环境问题跳过的用例，需要在更新的内核上运行
listmount04 listmount04
name_to_handle_at03 name_to_handle_at03
statmount09 statmount09
mount08 mount08

# 废弃的系统调用，现在都用getdents64替代
readdir21 readdir21

# 这些测试用例明确跳过fuse
file_attr01 file_attr01
file_attr02 file_attr02
file_attr03 file_attr03
file_attr04 file_attr04
file_attr05 file_attr05
ioctl_fiemap01 ioctl_fiemap01
openat02 openat02
unlink09 unlink09
mount03 mount03

# fuse内核不支持的测试项
fanotify10 fanotify10
fanotify13 fanotify13
fanotify16 fanotify16
fanotify18 fanotify18
fanotify19 fanotify19

# juicefs不支持FS_NOCOW_FL flag
fallocate06 fallocate06
```
