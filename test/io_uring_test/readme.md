## JuiceFS io_uring 测试结果总结

**测试环境**：Linux 内核 6.8-generic、JuiceFS 1.4-beta2

### 总览：当前共 12 项测试

| 测试套件 | 用例数 | 说明 |
|---------|--------|------|
| **Basic I/O** | 4 | 基础读写、readv/writev、批量提交、一致性校验 |
| **Fixed Buffers** | 3 | 固定缓冲区注册、读写、跨索引验证 |
| **Registered Files** | 3 | 固定文件表注册与配合固定缓冲区读写 |
| **Splice** | 2 | file->pipe、pipe->file、偏移小块传输、tee |

---

### 支持情况详细说明

1. **概述**

    本套件验证的是 Linux io_uring 请求在 JuiceFS 场景下的可用性与语义正确性，覆盖从常规 I/O 到高级 opcode 的主要路径。

2. **基础能力**

    `test_basic_io.c` 覆盖并验证以下能力：

    - `IORING_OP_READ/WRITE` 的基本正确性
    - `IORING_OP_READV/WRITEV` 的向量化读写语义
    - 批量提交多个 SQE 后的完成映射（`user_data`）
    - 写后读一致性校验

    > basic io 最终还是以 普通 read/write 请求的模式到达juicefs，可以正常享受 io_uring带来的性能优化

3. **固定资源能力（buffer/file registration）**

    - fixed_buffers 是指预注册一组缓冲区buffer 到 io_uring中，提高高频次io的 地址解析效率
    - file registration 是预注册一组 fd 到 io_uring中，提前预存 file 结构体，减少 fget 等锁消耗

    > 上述两个特性均由 io_uring组件层完成，fuse层无需特殊适配，juicefs 可以正常享受优化效果

4. **高级特性说明**

    | 特性 | 说明 |
    |---|---|
    | `IORING_OP_SPLICE` | 将文件描述符直接从一个进程空间传输到另一个进程空间，无需通过用户空间缓冲区 |
    | `IORING_OP_NOP` | 无操作，用于填充 io_uring 队列，不触发任何 I/O 操作 |
    | `IORING_OP_TIMEOUT` | 设置超时时间，用于等待 I/O 操作完成 |
    | `IORING_OP_TIMEOUT_REMOVE` | 移除超时时间，用于取消等待 I/O 操作的超时设置 |
    | `IORING_OP_LINK` | 将文件描述符链接到另一个文件描述符，无需通过用户空间缓冲区 |
    | `IORING_OP_PROVIDE_BUFFERS` | 提供固定缓冲区，用于高频次 io 的地址解析效率 |
    | `IORING_OP_SYNC_FILE_RANGE` | 同步某个文件范围的 pagecache 到磁盘 |

    > 上述特性均由 linux 内核实现，fuse 层无需特殊适配，juicefs 可以正常享受优化效果

    **`IORING_SETUP_IOPOLL`**

    其作用是把该 ring 的 IO 完成方式从终端驱动切换成轮询（poll）驱动。它依赖于底层的 iopoll 接口，且多用在块设备直接读写下，绝大多数文件系统不涉及，juicefs 也不支持该特性。


### 运行方法

### 检查 io_uring 可用性

```bash
cat /proc/sys/kernel/io_uring_disabled
# 输出 0 表示已启用
# 如果不为 0，执行: echo 0 | sudo tee /proc/sys/kernel/io_uring_disabled
```

### 安装 liburing

```bash
# Ubuntu/Debian
sudo apt install liburing-dev

# CentOS/RHEL
sudo yum install liburing-devel

# 或从源码编译
git clone https://github.com/axboe/liburing.git
cd liburing && make && sudo make install
```

### 构建测试程序

```bash
cd test/io_uring_test
make
```

### 运行完整测试套件

```bash
./run_tests.sh /path/to/juicefs/mountpoint
```

说明：

- 建议将参数设置为待验证文件系统挂载点（例如 JuiceFS 挂载路径）
- 若未传参数，默认在 `/tmp/io_uring_test` 下创建工作目录
- 如需检查 io_uring 是否被禁用，可查看 `/proc/sys/kernel/io_uring_disabled`

### 测试代码位置

```text
test/io_uring_test/
├── common.h                 # 公共头文件与测试辅助函数
├── test_basic_io.c          # 基础 I/O（4 项）
├── test_fixed_buffers.c     # 固定缓冲区（3 项）
├── test_registered_files.c  # 注册文件（3 项）
├── test_splice.c            # Splice/Tee（2 项）
├── run_tests.sh
└── Makefile
```
