---
title: POSIX Compatibility
sidebar_position: 6
slug: /posix_compatibility
description: Learn how JuiceFS ensures POSIX compatibility through testing with pjdfstest and LTP.
---

JuiceFS ensures POSIX compatibility by using [pjdfstest](https://github.com/pjd/pjdfstest) and [Linux Test Project (LTP)](https://github.com/linux-test-project/ltp) for testing.

## Pjdfstest

Pjdfstest is a test suite that helps to test POSIX system calls. JuiceFS passed all of its latest 8,789 tests:

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

:::note
When running pjdfstest, you must disable the JuiceFS trash, because the test deletes files directly rather than moving them to the trash. The JuiceFS trash is enabled by default. To disable it, run `juicefs config <meta-url> --trash-days 0`.
:::

Besides the features covered by pjdfstest, JuiceFS provides:

- Close-to-open consistency. It ensures that once a file is written and closed, the written data is accessible in the following open and read operations. Within the same mount point, all written data can be read immediately.
- Rename and all other metadata operations are atomic, guaranteed by the transactional nature of metadata engines.
- Open files remain accessible after being unlinked from the same mount point.
- Mmap (tested with FSx).
- Fallocate with punch hole support.
- Extended attributes (xattr).
- BSD locks (flock).
- POSIX traditional record locks (fcntl).

:::note
POSIX record locks are classified as **traditional locks** ("process-associated") and **OFD locks** (open file description locks). Their locking operation commands are `F_SETLK` and `F_OFD_SETLK` respectively. Due to the implementation of the FUSE kernel module, JuiceFS currently only supports traditional record locks. More details can be found at: [https://man7.org/linux/man-pages/man2/fcntl.2.html](https://man7.org/linux/man-pages/man2/fcntl.2.html).
:::

## LTP

LTP is a joint project developed and maintained by IBM, Cisco, Fujitsu, and others.

> The project goal is to deliver tests to the open source community that validates the reliability, robustness, and stability of Linux.
>
> The LTP testsuite contains a collection of tools for testing the Linux kernel and related features. Our goal is to improve the Linux kernel and system libraries by bringing test automation to the testing effort.

JuiceFS passed most of the file system related tests.

### Test environment

- Host: Aliyun ESC server
- OS: Ubuntu 24.04 (Kernel `6.8.0-55-generic`)
- JuiceFS version: 1.4-beta2
- LTP version: 20260529

### Test steps

1. Download the LTP [release](https://github.com/linux-test-project/ltp/releases/download/20260529/ltp-full-20260529.tar.xz) from GitHub.

2. Unarchive, compile, and install LTP:

   ```bash
   tar -xvf ltp-full-20260529.tar.xz
   cd ltp-full-20260529
   ./configure
   make all
   make install
   ```

3. Install kirk (the new LTP test runner that replaces the removed runltp):

   ```bash
   pip install kirk
   ```

4. Change the directory to `/opt/ltp` where the test tools are installed:

   ```bash
   cd /opt/ltp
   ```

5. Create test configuration files. To simplify testing, delete pressure tests, filesystem‑unrelated entries, and tests that do not run on JuiceFS from `runtest/fs` and `runtest/syscalls`. Then, save the modified files as `fs‑jfs` and `syscalls‑jfs`. (See [Appendix](#appendix) for the detailed deletion list.)

6. Execute tests:

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

### Test result

| Test suite | Tests run | Passed | Failed | Broken | Skipped |
|------------|------|--------|--------|--------|---------|
| fs_bind | 95 | 95 | 0 | 0 | 0 |
| fs_perms_simple | 18 | 18 | 0 | 0 | 0 |
| smoketest | 12 | 10 | 0 | 0 | 2 |
| fs-jfs | 30 | 30 | 0 | 0 | 0 |
| syscalls-jfs | 1323 | 1300 | 0 | 0 | 23 |
| fcntl-locktests | 1 | 1 | 0 | 0 | 0 |
| **Total** | **1479** | **1454** | **0** | **0** | **25** |

All file system-related tests run on JuiceFS passed, with 0 failures and 0 broken tests.

### Appendix

Here are deleted cases in `fs` and `syscalls`:

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
# A large number of test cases unrelated to file systems were removed from the syscalls suite. Only some cases unsupported by FUSE or JuiceFS are listed here.
# For the complete list, please refer to .github/workflows/bash/rm_syscalls

# Pure kernel functionality tests, unrelated to userspace filesystems.
close_range01 close_range01
close_range02 close_range02
openat201 openat201
openat202 openat202
openat203 openat203

# CVE regression tests (SELINUX), unrelated to JuiceFS
listxattr04 listxattr04
io_uring02 io_uring02

# Tests skipped due to environment limitations; they require a newer kernel version
listmount04 listmount04
name_to_handle_at03 name_to_handle_at03
statmount09 statmount09
mount08 mount08

# Deprecated system calls, now replaced by getdents64
readdir21 readdir21

# Tests explicitly skipping FUSE
file_attr01 file_attr01
file_attr02 file_attr02
file_attr03 file_attr03
file_attr04 file_attr04
file_attr05 file_attr05
ioctl_fiemap01 ioctl_fiemap01
openat02 openat02
unlink09 unlink09
mount03 mount03

# Tests unsupported by the FUSE kernel module
fanotify10 fanotify10
fanotify13 fanotify13
fanotify16 fanotify16
fanotify18 fanotify18
fanotify19 fanotify19

# JuiceFS does not support the FS_NOCOW_FL flag
fallocate06 fallocate06
```
