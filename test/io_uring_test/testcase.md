# JuiceFS io_uring 测试套件

本测试套件用于验证 JuiceFS 文件系统对 Linux io_uring 异步 I/O 接口的支持情况。测试覆盖了从基础读写到高级特性的广泛场景。

## 目录

- [环境要求](#环境要求)
- [构建与运行](#构建与运行)
- [测试架构](#测试架构)
- [测试用例详解](#测试用例详解)
  - [1. 基础 I/O 测试 (test_basic_io)](#1-基础-io-测试-test_basic_io)
  - [2. 固定缓冲区测试 (test_fixed_buffers)](#2-固定缓冲区测试-test_fixed_buffers)
  - [3. 注册文件测试 (test_registered_files)](#3-注册文件测试-test_registered_files)
  - [4. Splice 测试 (test_splice)](#4-splice-测试-test_splice)
  - [5. 文件操作测试 (test_file_ops)](#5-文件操作测试-test_file_ops)
  - [6. 目录操作测试 (test_dir_ops)](#6-目录操作测试-test_dir_ops)
  - [7. 高级特性测试 (test_advanced)](#7-高级特性测试-test_advanced)

## 环境要求

- Linux 操作系统（内核版本 5.1+ 基础支持，5.6+ 推荐以获得完整功能）
- GCC 编译器
- liburing 开发库（liburing-dev / liburing-devel）
- 已挂载的 JuiceFS 文件系统（作为测试目录）
- io_uring 内核功能未禁用（`/proc/sys/kernel/io_uring_disabled` 值为 0）

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

## 构建与运行

### 构建测试程序

```bash
cd test/io_uring_test
make
```

### 运行完整测试套件

```bash
./run_tests.sh /path/to/juicefs/mountpoint
```

**注意**: 默认测试目录为 `/tmp/io_uring_test`，建议指定 JuiceFS 挂载点作为参数。

### 运行单个测试程序

```bash
./test_basic_io /path/to/juicefs/mountpoint
./test_fixed_buffers /path/to/juicefs/mountpoint
./test_registered_files /path/to/juicefs/mountpoint
./test_splice /path/to/juicefs/mountpoint
./test_file_ops /path/to/juicefs/mountpoint
./test_dir_ops /path/to/juicefs/mountpoint
./test_advanced /path/to/juicefs/mountpoint
```

### 清理构建产物

```bash
make clean
```

## 测试架构

### 公共模块 (common.h)

所有测试程序共享公共头文件，提供：

- **常量定义**:
  - `QUEUE_DEPTH`: 64（io_uring 提交队列深度）
  - `BLOCK_SIZE`: 4096 字节（标准块大小）
  - `TEST_FILE_SIZE`: 128KB（测试文件大小）

- **结果枚举**:
  - `TEST_PASS` (0): 测试通过
  - `TEST_FAIL` (1): 测试失败
  - `TEST_SKIP` (2): 测试跳过

- **宏定义**:
  - `TEST_PASS_MSG(name)`: 标记测试通过并输出信息
  - `TEST_FAIL_MSG(name, ...)`: 标记测试失败并输出详细信息
  - `TEST_SKIP_MSG(name, ...)`: 标记测试跳过（功能不支持时）

- **工具函数**:
  - `create_test_file()`: 创建指定大小的测试文件，填充 A-Z 循环模式数据
  - `init_ring()`: 初始化 io_uring 实例（封装 `io_uring_queue_init`）
  - `submit_and_wait()`: 提交 SQE 并等待一个 CQE 完成
  - `submit_and_wait_timeout()`: 带超时的提交等待
  - `print_summary()`: 输出测试结果统计摘要

### io_uring 基本编程模型

```
应用程序                    内核
   |                         |
   |--- 获取 SQE ----------->|  提交队列 (SQ)
   |--- 填充操作参数 -------->|
   |--- io_uring_submit() -->|
   |                         |--- 处理 I/O 请求
   |<-- io_uring_wait_cqe() -|  完成队列 (CQ)
   |--- 读取 CQE 结果 ------>|
   |--- io_uring_cqe_seen() -|  通知已消费
```

### 测试结果判定

| 状态 | 含义 | 说明 |
|------|------|------|
| `[PASS]` | 通过 | 功能正常工作 |
| `[FAIL]` | 失败 | 功能异常或数据不一致 |
| `[SKIP]` | 跳过 | 当前环境/内核不支持该功能（非错误） |

---

## 测试用例详解

### 1. 基础 I/O 测试 (test_basic_io)

**目标**: 验证 io_uring 基本读写操作的正确性

**测试数量**: 4 个

#### 1.1 test_basic_read - 基础异步读取（含偏移语义）

**描述**: 使用 `IORING_OP_READ` 验证基础读取和带偏移读取语义

**测试步骤**:
1. 初始化 io_uring 实例（队列深度 64，无特殊标志）
2. 打开预创建的 128KB 测试文件（O_RDONLY）
3. 获取一个 SQE（Submission Queue Entry）
4. 使用 `io_uring_prep_read()` 准备读取操作
5. 设置 user_data 为 1（用于标识此请求）
6. 提交并等待完成
7. 验证第一个 CQE（Completion Queue Entry）结果：
   - 返回值等于 BLOCK_SIZE（4096 字节）
   - 读取的数据与源文件一致（A-Z 循环模式）
   - `user_data` 与提交时一致
8. 再次提交带偏移读取（offset=BLOCK_SIZE）并验证：
   - 返回值等于 BLOCK_SIZE
   - 读取内容与偏移位置的数据模式一致
   - `user_data` 与提交时一致
9. 检查文件当前位置未被带偏移读取改变

**验证点**:
- ✅ io_uring 实例初始化成功
- ✅ SQE 获取和填充正确
- ✅ 异步提交和完成等待正常
- ✅ 读取数据量正确
- ✅ 数据完整性校验通过
- ✅ 带偏移读取的数据正确
- ✅ 带偏移读取不影响文件当前位置
- ✅ `user_data` 回传正确

**预期结果**: `[PASS]` - 基础读取与偏移读取语义均正确

---

#### 1.2 test_readv - 向量读取（Scatter Read）

**描述**: 使用 `IORING_OP_READV` 将文件数据分散读取到多个缓冲区

**测试步骤**:
1. 初始化 io_uring 实例
2. 打开测试文件（O_RDONLY）
3. 准备 2 个 iovec（各 2048 字节，共 4096 字节）
4. 使用 `io_uring_prep_readv()` 准备向量读取
5. 提交并等待完成
6. 验证 CQE 返回读取字节数为 4096
7. 校验两个 iovec 中的数据内容与预期偏移模式一致

**验证点**:
- ✅ io_uring 支持向量读取操作
- ✅ 多个 iovec 正确填充
- ✅ 总读取量等于各 iovec 长度之和
- ✅ 数据按 iovec 顺序分散存储
- ✅ 每个 iovec 的数据内容正确

**预期结果**: `[PASS]` - 向量读取正常工作

---

#### 1.3 test_writev - 向量写入（Gather Write）

**描述**: 使用 `IORING_OP_WRITEV` 将多个缓冲区的数据聚集写入文件

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建新文件
3. 准备 2 个 iovec（各 2048 字节），分别填充 'X' 和 'Y'
4. 使用 `io_uring_prep_writev()` 准备向量写入
5. 提交并等待完成
6. 使用普通 `read()` 读回数据
7. 验证前 2048 字节为 'X'，后 2048 字节为 'Y'

**验证点**:
- ✅ io_uring 支持向量写入操作
- ✅ 多个 iovec 按顺序写入
- ✅ 数据边界精确对齐
- ✅ 写入后数据完整可验证

**预期结果**: `[PASS]` - 向量写入数据正确

---

#### 1.4 test_batch_io - 批量并行 I/O

**描述**: 一次性提交 4 个读取请求，验证 io_uring 的批量处理与完成映射能力

**测试步骤**:
1. 初始化 io_uring 实例
2. 打开测试文件
3. 分配 4 个缓冲区（各 4096 字节）
4. 循环获取 4 个 SQE，分别准备从不同偏移量读取：
   - `io_uring_prep_read(sqe, fd, bufs[i], BLOCK_SIZE, i * BLOCK_SIZE)`
5. 一次性提交所有 4 个请求（`io_uring_submit()`）
6. 逐一等待 4 个 CQE 完成
7. 通过 `user_data` 将 CQE 映射回请求索引，验证：
   - 每个请求只完成一次（无重复）
   - 返回值均为 BLOCK_SIZE
   - 每个缓冲区的数据内容与其偏移位置匹配

**验证点**:
- ✅ 支持批量提交多个 SQE
- ✅ 多个 I/O 请求可以并行执行
- ✅ 所有请求均正确完成
- ✅ 每个请求的数据量正确
- ✅ 通过 `user_data` 正确映射完成事件到请求
- ✅ 每个请求返回的数据内容正确

**技术要点**:
- 批量提交是 io_uring 的核心优势之一
- 相比传统 I/O 逐个提交，减少了系统调用次数
- 内核可以优化多个请求的执行顺序

**预期结果**: `[PASS]` - 4 个并行读取请求全部成功且映射正确

---

#### 1.5 test_read_write_consistency - 读写一致性验证

**描述**: 先写入数据再读回，验证 io_uring 读写操作的数据一致性

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建新文件
3. 准备写入缓冲区（0x00-0xFF 循环模式，4096 字节）
4. 使用 `IORING_OP_WRITE` 写入数据
5. 等待写入完成
6. 使用 `IORING_OP_READ` 读回数据
7. 比较写入和读回的缓冲区

**验证点**:
- ✅ 写入操作正确完成
- ✅ 读取操作正确完成
- ✅ 写入和读回的数据完全一致
- ✅ 适合验证 io_uring 读写路径的端到端正确性

**预期结果**: `[PASS]` - 读写数据完全一致

---

### 2. 固定缓冲区测试 (test_fixed_buffers)

**目标**: 验证 io_uring 注册缓冲区（Registered Buffers）机制的正确性

**前置知识**: io_uring 允许应用程序预先注册一组缓冲区到内核，后续 I/O 操作通过索引引用这些缓冲区，避免每次 I/O 都进行内存映射/解除映射操作，从而提升性能。

**测试数量**: 4 个

#### 2.1 test_register_buffers_lifecycle - 注册缓冲区生命周期

**描述**: 在同一用例中验证单缓冲区与多缓冲区的注册/注销流程

**测试步骤**:
1. 初始化 io_uring 实例
2. 使用 `posix_memalign()` 分配 4 个 4096 字节对齐的缓冲区
3. 调用 `io_uring_register_buffers()` 注册 1 个缓冲区并注销
4. 调用 `io_uring_register_buffers()` 注册 4 个缓冲区并注销
5. 释放缓冲区内存

**验证点**:
- ✅ 单缓冲区注册/注销成功
- ✅ 多缓冲区批量注册/注销成功
- ✅ 注册/注销生命周期完整且结果可判定

**技术要点**:
- 注册缓冲区需要页对齐（通常 4096 字节边界）
- 注册后内核会锁定这些内存页，防止被换出
- 单测合并后避免重复初始化逻辑，提高可维护性

**预期结果**: `[PASS]` - 单缓冲区与多缓冲区生命周期均成功

---

#### 2.2 test_read_fixed - 固定缓冲区读取

**描述**: 使用 `IORING_OP_READ_FIXED` 通过注册缓冲区索引读取数据

**测试步骤**:
1. 初始化 io_uring 实例
2. 分配并注册 1 个页对齐缓冲区
3. 创建 128KB 测试文件
4. 打开文件（O_RDONLY）
5. 使用 `io_uring_prep_read_fixed(sqe, fd, buf, BLOCK_SIZE, BLOCK_SIZE, 0)` 读取
   - 最后一个参数 `0` 为注册缓冲区的索引
6. 提交并等待完成
7. 验证读取 4096 字节、`user_data` 回传与数据内容

**验证点**:
- ✅ `IORING_OP_READ_FIXED` 操作正常
- ✅ 通过缓冲区索引正确引用注册缓冲区
- ✅ 读取数据量正确
- ✅ 读取数据内容与测试文件模式一致
- ✅ `user_data` 回传正确
- ✅ 带偏移 fixed read 不改变文件当前位置

**与普通 READ 的区别**:
- `IORING_OP_READ`: 每次操作需要内核映射/解除映射用户缓冲区
- `IORING_OP_READ_FIXED`: 直接使用预注册的缓冲区，减少开销

**预期结果**: `[PASS]` - 固定缓冲区读取成功

---

#### 2.3 test_write_fixed - 固定缓冲区写入

**描述**: 使用 `IORING_OP_WRITE_FIXED` 通过注册缓冲区索引写入数据

**测试步骤**:
1. 初始化 io_uring 实例
2. 分配并注册 1 个页对齐缓冲区，填充字符 'Z'
3. 创建新文件
4. 使用 `io_uring_prep_write_fixed(sqe, fd, buf, BLOCK_SIZE, 0, 0)` 写入
5. 提交并等待完成
6. 验证 `user_data` 回传正确
7. 使用普通 `read()` 读回数据
8. 验证所有字节均为 'Z'

**验证点**:
- ✅ `IORING_OP_WRITE_FIXED` 操作正常
- ✅ 写入数据正确
- ✅ `user_data` 回传正确
- ✅ 数据持久化后可验证

**预期结果**: `[PASS]` - 固定缓冲区写入数据正确

---

#### 2.4 test_fixed_buffers_rw_consistency - 固定缓冲区读写一致性

**描述**: 使用同一注册缓冲区先写后读，验证数据一致性

**测试步骤**:
1. 初始化 io_uring 实例
2. 分配并注册 1 个页对齐缓冲区
3. 创建新文件
4. 向注册缓冲区填充数据（0x00-0xFF 循环）
5. 使用 `IORING_OP_WRITE_FIXED` 写入
6. 清零缓冲区
7. 使用 `IORING_OP_READ_FIXED` 读回
8. 验证写入与读取 CQE 的 `user_data`
9. 比较原始数据和读回数据

**验证点**:
- ✅ 同一缓冲区可复用于写和读操作
- ✅ 写入后读回的数据完全一致
- ✅ 缓冲区索引引用正确
- ✅ CQE 返回与请求映射一致

**预期结果**: `[PASS]` - 固定缓冲区读写数据一致

---

#### 2.5 test_fixed_buffers_multiple_indices - 多索引固定缓冲区

**描述**: 注册 3 个缓冲区，使用不同索引并发读取并校验完成映射

**测试步骤**:
1. 初始化 io_uring 实例
2. 分配并注册 3 个页对齐缓冲区
3. 创建 128KB 测试文件
4. 提交 3 个 `IORING_OP_READ_FIXED` 请求：
   - 索引 0: 从偏移 0 读取
   - 索引 1: 从偏移 4096 读取
   - 索引 2: 从偏移 8192 读取
5. 等待 3 个 CQE 完成并按 `user_data` 映射回请求
6. 验证每个索引仅完成一次，且数据内容与偏移一致

**验证点**:
- ✅ 多个注册缓冲区索引可独立使用
- ✅ `user_data` 与请求一一映射且无重复完成
- ✅ 不同索引读取的数据内容正确
- ✅ 批量提交和完成均正确

**预期结果**: `[PASS]` - 3 个索引的固定缓冲区读取均成功

---

### 3. 注册文件测试 (test_registered_files)

**目标**: 验证 io_uring 注册文件（Registered Files）机制的正确性

**前置知识**: io_uring 允许应用程序预先注册一组文件描述符到内核，后续 I/O 操作通过索引引用，避免每次操作都进行 fd 查找，减少内核开销。

**测试数量**: 4 个

#### 3.1 test_register_files_lifecycle_update - 注册文件生命周期与动态更新

**描述**: 在同一用例中验证批量注册/注销与 `IORING_REGISTER_FILES_UPDATE` 生效性

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建 4 个临时文件并注册，随后注销
3. 重新注册前 2 个文件
4. 调用 `io_uring_register_files_update()` 将槽位 0 更新为第 3 个文件
5. 提交 `IOSQE_FIXED_FILE` 读取（fd=0）验证更新后的槽位真正生效
6. 注销并清理文件

**验证点**:
- ✅ 批量注册/注销生命周期正确
- ✅ `IORING_REGISTER_FILES_UPDATE` 返回值正确（更新条目数）
- ✅ 更新后的固定文件槽位可被实际 I/O 使用
- ✅ 读取数据内容与偏移模式一致，非仅检查返回长度

**预期结果**: `[PASS]` - 注册生命周期与动态更新均成功

---

#### 3.2 test_read_with_fixed_file - 固定文件读取

**描述**: 使用 `IOSQE_FIXED_FILE` 标志通过注册文件索引执行读取

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建 128KB 测试文件
3. 打开文件并注册该 fd
4. 使用 `io_uring_prep_read(sqe, 0, buf, BLOCK_SIZE, 0)` 准备读取
   - 第一个参数 `0` 为注册文件的索引（而非真实 fd）
5. 设置 `IOSQE_FIXED_FILE` 标志
6. 提交并等待完成
7. 验证读取 4096 字节、`user_data` 回传与数据内容

**验证点**:
- ✅ `IOSQE_FIXED_FILE` 标志正确使用
- ✅ 通过文件索引而非 fd 执行 I/O
- ✅ 读取数据量正确
- ✅ `user_data` 回传正确
- ✅ 读取内容与测试文件模式一致
- ✅ 带偏移读取不改变文件当前位置

**技术要点**:
- 使用固定文件时，SQE 中的 fd 字段变为注册数组的索引
- 必须设置 `IOSQE_FIXED_FILE` 标志告知内核
- 减少了内核从 fd 查找 file 结构的开销

**预期结果**: `[PASS]` - 固定文件读取成功

---

#### 3.3 test_write_with_fixed_file - 固定文件写入

**描述**: 使用 `IOSQE_FIXED_FILE` 标志通过注册文件索引执行写入

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建新文件并注册该 fd
3. 准备写入缓冲区（字符 'Q'，4096 字节）
4. 使用 `io_uring_prep_write(sqe, 0, buf, BLOCK_SIZE, 0)` 写入
5. 设置 `IOSQE_FIXED_FILE` 标志
6. 提交并等待完成
7. 验证 `user_data` 回传正确
8. 使用普通 `read()` 读回数据并验证

**验证点**:
- ✅ 固定文件写入操作正常
- ✅ 写入数据正确持久化
- ✅ `user_data` 回传正确
- ✅ 数据完整性校验通过

**预期结果**: `[PASS]` - 固定文件写入数据正确

---

#### 3.4 test_fixed_file_with_fixed_buffer - 固定文件 + 固定缓冲区

**描述**: 同时使用注册文件和注册缓冲区执行 I/O 操作

**测试步骤**:
1. 初始化 io_uring 实例
2. 分配并注册 1 个页对齐缓冲区
3. 创建 128KB 测试文件
4. 打开文件并注册该 fd
5. 使用 `io_uring_prep_read_fixed(sqe, 0, buf, BLOCK_SIZE, 0, 0)` 读取
   - fd 参数为 0（注册文件索引）
   - 缓冲区索引为 0（注册缓冲区索引）
6. 设置 `IOSQE_FIXED_FILE` 标志
7. 提交并等待完成
8. 验证读取 4096 字节、`user_data` 回传与数据内容

**验证点**:
- ✅ 注册文件和注册缓冲区可同时使用
- ✅ `IOSQE_FIXED_FILE` + `IORING_OP_READ_FIXED` 组合正常
- ✅ 双重优化路径正确
- ✅ `user_data` 与请求映射正确
- ✅ 读取内容与测试文件模式一致

**技术要点**:
- 这是 io_uring 最高效的 I/O 路径
- 同时避免了 fd 查找和内存映射的开销
- 适合高性能 I/O 密集型应用

**预期结果**: `[PASS]` - 固定文件 + 固定缓冲区读取成功

---


---

### 4. Splice 测试 (test_splice)

**目标**: 验证 io_uring 的零拷贝数据传输操作（splice 和 tee）

**前置知识**: splice 是 Linux 的零拷贝机制，允许数据在文件和管道之间直接传输，无需经过用户空间缓冲区。

**测试数量**: 4 个

#### 4.1 test_splice_file_to_pipe - 文件到管道的 Splice

**描述**: 使用 `IORING_OP_SPLICE` 将文件数据零拷贝传输到管道

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建文件并写入 4096 字节数据（字符 'S'）
3. 创建管道（pipe）
4. 使用 `io_uring_prep_splice(sqe, fd, 0, pipefd[1], -1, BLOCK_SIZE, 0)` 传输
   - 从文件偏移 0 读取
   - 写入管道写端
   - 管道偏移设为 -1（管道不支持偏移）
5. 提交并等待完成
6. 验证 CQE 的 `user_data` 和返回字节数
7. 从管道读端读取数据
8. 验证传输数据与原始数据一致

**验证点**:
- ✅ `IORING_OP_SPLICE` 操作正常
- ✅ CQE 映射正确（`user_data`）
- ✅ 数据从文件正确传输到管道
- ✅ 零拷贝传输数据完整
- ✅ 带 offset splice 不改变文件当前位置

**技术要点**:
- splice 不涉及用户空间的数据拷贝
- 数据直接在内核空间从文件页缓存传输到管道缓冲区
- 适合大文件传输场景

**预期结果**: `[PASS]` - 文件到管道的零拷贝传输成功

---

#### 4.2 test_splice_pipe_to_file - 管道到文件的 Splice

**描述**: 使用 `IORING_OP_SPLICE` 将管道数据零拷贝传输到文件

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建管道并向写端写入 4096 字节数据（字符 'P'）
3. 创建新文件
4. 使用 `io_uring_prep_splice(sqe, pipefd[0], -1, fd, 0, BLOCK_SIZE, 0)` 传输
   - 从管道读端读取
   - 写入文件偏移 0
5. 提交并等待完成
6. 验证 CQE 的 `user_data` 和返回字节数
7. 使用普通 `read()` 从文件读回数据
8. 验证传输数据与原始数据一致

**验证点**:
- ✅ 管道到文件的零拷贝传输正常
- ✅ CQE 映射正确（`user_data`）
- ✅ 数据正确持久化到文件
- ✅ 数据完整性校验通过

**预期结果**: `[PASS]` - 管道到文件的零拷贝传输成功

---

#### 4.3 test_splice_offset_and_small_chunks - 带偏移小块 Splice

**描述**: 从文件偏移位置按 8 个 512B 小块并发执行 splice，验证完成映射和总量

**测试步骤**:
1. 初始化 io_uring 实例
2. 在文件偏移 8192 处写入 4096 字节测试数据
3. 创建管道
4. 批量提交 8 个 `IORING_OP_SPLICE`（每个 512 字节，偏移递增）
5. 等待 8 个 CQE 并按 `user_data` 映射回请求
6. 校验每个 chunk 只完成一次、返回值均为 512
7. 从管道读出 4096 字节并校验整体数据一致

**验证点**:
- ✅ 支持指定文件偏移量的 splice
- ✅ 支持小块数据批量 splice
- ✅ `user_data` 映射正确且无重复完成
- ✅ 总传输量正确（8 × 512 = 4096）
- ✅ 读取数据与源数据一致

**预期结果**: `[PASS]` - 带偏移小块 splice 成功且映射正确

---

#### 4.4 test_tee - 管道数据复制（Tee）

**描述**: 使用 `IORING_OP_TEE` 在两个管道之间复制数据（不消耗源管道数据）

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建两个管道（pipe_in 和 pipe_out）
3. 向 pipe_in 写端写入 4096 字节数据（字符 'T'）
4. 使用 `io_uring_prep_tee(sqe, pipe_in[0], pipe_out[1], BLOCK_SIZE, 0)` 复制
5. 提交并等待完成
6. 验证 CQE 的 `user_data` 和返回字节数
7. 从 pipe_in 读端读取数据（原始数据仍在）
8. 从 pipe_out 读端读取数据（复制的数据）
9. 验证两份数据均与原始数据一致

**验证点**:
- ✅ `IORING_OP_TEE` 操作正常
- ✅ CQE 映射正确（`user_data`）
- ✅ 源管道数据不被消耗
- ✅ 目标管道获得数据副本
- ✅ 两份数据均完整正确

**splice vs tee 区别**:
- `splice`: 移动数据，源端数据被消耗
- `tee`: 复制数据，源端数据保留

**预期结果**: `[PASS]` - 管道数据复制成功，两端数据一致

---


---

### 5. 文件操作测试 (test_file_ops)

**目标**: 验证 io_uring 支持的文件管理操作

**测试数量**: 5 个

#### 5.1 test_openat_variants - 异步打开文件（openat + openat2）

**描述**: 在同一用例中分别使用 `IORING_OP_OPENAT` 和 `IORING_OP_OPENAT2`，验证两种打开路径均可达且返回映射正确

**测试步骤**:
1. 初始化 io_uring 实例
2. 提交 `openat` 请求并设置 `user_data=1`
3. 校验 CQE：`user_data` 映射正确且返回有效 fd
4. 提交 `openat2` 请求并设置 `user_data=2`
5. 校验 CQE：`user_data` 映射正确且返回有效 fd
6. 校验两个目标文件均被创建，随后关闭 fd 并清理

**验证点**:
- ✅ `IORING_OP_OPENAT` 与 `IORING_OP_OPENAT2` 均可达
- ✅ CQE `user_data` 可准确映射两类请求
- ✅ 两种路径都返回有效 fd 且创建目标文件

**技术要点**:
- 合并后降低冗余，同时保留两条系统调用路径覆盖

**预期结果**: `[PASS]` - openat/openat2 异步打开均成功

---

#### 5.2 test_close - 异步关闭文件

**描述**: 使用 `IORING_OP_CLOSE` 通过 io_uring 异步关闭文件描述符

**测试步骤**:
1. 使用普通 `open()` 打开文件获取 fd
2. 初始化 io_uring 实例
3. 使用 `io_uring_prep_close(sqe, fd)` 关闭
4. 提交并等待完成
5. 验证 CQE 返回成功

**验证点**:
- ✅ `IORING_OP_CLOSE` 操作正常
- ✅ 文件描述符被正确关闭

**预期结果**: `[PASS]` - 异步关闭文件成功

---

#### 5.3 test_statx - 异步获取文件状态

**描述**: 使用 `IORING_OP_STATX` 通过 io_uring 异步获取文件元数据

**测试步骤**:
1. 创建文件并写入 "statx test data"（15 字节）
2. 初始化 io_uring 实例
3. 使用 `io_uring_prep_statx(sqe, AT_FDCWD, filepath, 0, STATX_ALL, &stxbuf)` 获取
4. 提交并等待完成
5. 验证返回的 statx 结构：
   - `stx_mask` 包含 `STATX_SIZE` 标志
   - `stx_size` 等于 15 字节

**验证点**:
- ✅ `IORING_OP_STATX` 操作正常
- ✅ 返回完整的文件元数据
- ✅ 文件大小字段正确
- ✅ `STATX_ALL` 请求的所有属性均返回

**预期结果**: `[PASS]` - 异步获取文件状态成功

---

#### 5.4 test_sync_variants - 异步同步（fsync + fdatasync）

**描述**: 批量提交 `IORING_OP_FSYNC`（flags=0）和 `IORING_OP_FSYNC`（`IORING_FSYNC_DATASYNC`）并校验完成映射

**测试步骤**:
1. 创建两个测试文件并写入 4096 字节数据
2. 初始化 io_uring 实例
3. 提交两个同步请求并分别设置 `user_data`
4. 等待两个 CQE，校验每类请求只完成一次
5. 回读文件验证数据仍可正确读取

**验证点**:
- ✅ fsync/fdatasync 两条路径均可达
- ✅ `user_data` 映射正确无重复完成
- ✅ 同步后文件可回读且内容一致

**fsync vs fdatasync 区别**:
- `fsync` (flags=0): 同步数据 + 元数据
- `fdatasync` (IORING_FSYNC_DATASYNC): 仅同步数据，性能更优

**预期结果**: `[PASS]` - fsync/fdatasync 异步同步均成功

---

#### 5.5 test_fallocate - 异步空间预分配

**描述**: 使用 `IORING_OP_FALLOCATE` 通过 io_uring 异步预分配文件空间

**测试步骤**:
1. 创建文件
2. 初始化 io_uring 实例
3. 使用 `io_uring_prep_fallocate(sqe, fd, 0, 0, BLOCK_SIZE * 4)` 预分配 16384 字节
4. 提交并等待完成
5. 使用 `fstat()` 验证文件大小为 16384 字节

**验证点**:
- ✅ `IORING_OP_FALLOCATE` 操作正常
- ✅ 文件空间被正确预分配
- ✅ 文件大小与请求一致

**预期结果**: `[PASS]` - 异步空间预分配成功

---

### 6. 目录操作测试 (test_dir_ops)

**目标**: 验证 io_uring 支持的目录和文件系统操作

**测试数量**: 4 个

#### 6.1 test_mkdirat - 异步创建目录

**描述**: 使用 `IORING_OP_MKDIRAT` 通过 io_uring 异步创建目录

**测试步骤**:
1. 初始化 io_uring 实例
2. 使用 `io_uring_prep_mkdirat(sqe, AT_FDCWD, dirpath, 0755)` 创建
3. 提交并等待完成
4. 使用 `stat()` 验证目录已创建且类型为目录（S_ISDIR）

**验证点**:
- ✅ `IORING_OP_MKDIRAT` 操作正常
- ✅ 目录被正确创建
- ✅ 目录权限设置正确

**预期结果**: `[PASS]` - 异步创建目录成功

---

#### 6.2 test_unlinkat_variants - 异步删除（文件 + 目录）

**描述**: 在同一用例中批量提交两个 `IORING_OP_UNLINKAT`，分别删除普通文件与目录（`AT_REMOVEDIR`）

**测试步骤**:
1. 分别创建临时文件和临时目录
2. 初始化 io_uring 实例
3. 批量提交两个 unlinkat 请求，并设置不同 `user_data`
4. 逐个等待 CQE，验证映射正确且无重复完成
5. 验证文件和目录均不存在

**验证点**:
- ✅ 文件删除与目录删除两条路径均可达
- ✅ `user_data` 映射可区分 file/dir 请求
- ✅ 两类目标都被正确删除

**预期结果**: `[PASS]` - 文件与目录异步删除均成功

---

#### 6.3 test_renameat - 异步重命名

**描述**: 使用 `IORING_OP_RENAMEAT` 通过 io_uring 异步重命名文件

**测试步骤**:
1. 创建源文件（renameat_old）
2. 初始化 io_uring 实例
3. 使用 `io_uring_prep_renameat(sqe, AT_FDCWD, oldpath, AT_FDCWD, newpath, 0)` 重命名
4. 提交并等待完成
5. 验证旧路径不存在，新路径存在
6. 回读新路径数据，验证内容保持不变

**验证点**:
- ✅ `IORING_OP_RENAMEAT` 操作正常
- ✅ 旧文件名不再存在
- ✅ 新文件名可以访问
- ✅ 重命名前后内容保持一致

**预期结果**: `[PASS]` - 异步重命名成功

---

#### 6.4 test_link_and_symlink - 异步创建链接（硬链接 + 符号链接）

**描述**: 批量提交 `IORING_OP_LINKAT` 与 `IORING_OP_SYMLINKAT`，验证完成映射与语义差异

**测试步骤**:
1. 创建目标文件并写入测试数据
2. 初始化 io_uring 实例
3. 批量提交硬链接和符号链接请求，并设置不同 `user_data`
4. 等待两个 CQE，验证映射正确
5. 用 `stat()` 验证硬链接 inode 与目标一致，且 `nlink >= 2`
6. 用 `lstat()` 验证符号链接类型为 `S_ISLNK`
7. 从硬链接回读内容，验证数据可达

**验证点**:
- ✅ `IORING_OP_LINKAT` 与 `IORING_OP_SYMLINKAT` 均可达
- ✅ `user_data` 映射正确无重复完成
- ✅ 硬链接 inode 语义与链接计数正确
- ✅ 符号链接类型正确
- ✅ 硬链接路径可读且内容一致

**硬链接 vs 符号链接**:
- 硬链接：指向相同的 inode，不能跨文件系统
- 符号链接：指向路径名，可以跨文件系统

**预期结果**: `[PASS]` - 硬链接与符号链接异步创建均成功

---

### 7. 高级特性测试 (test_advanced)

**目标**: 验证 io_uring 的高级特性，包括超时、链式请求、轮询、缓冲区提供等

**测试数量**: 9 个

#### 7.1 test_nop - 空操作

**描述**: 使用 `IORING_OP_NOP` 提交一个无操作的请求，验证基本机制

**测试步骤**:
1. 初始化 io_uring 实例
2. 使用 `io_uring_prep_nop(sqe)` 准备空操作
3. 设置 user_data 为 0xFF
4. 提交并等待完成
5. 验证 CQE：
   - res 字段为 0（成功）
   - user_data 为 0xFF（与提交时一致）

**验证点**:
- ✅ `IORING_OP_NOP` 正常完成
- ✅ user_data 正确传递和返回
- ✅ 基本 SQE/CQE 机制正常

**用途**:
- 验证 io_uring 基础设施是否正常
- 测量 io_uring 的最小延迟
- 作为链式请求的占位符

**预期结果**: `[PASS]` - 空操作正常完成

---

#### 7.2 test_timeout_variants - 超时与取消

**描述**: 在同一用例中验证 `IORING_OP_TIMEOUT` 超时触发与 `IORING_OP_TIMEOUT_REMOVE` 取消语义

**测试步骤**:
1. 初始化 io_uring 实例
2. 提交 100ms 超时请求（`user_data=1`）并验证返回 `-ETIME`
3. 提交 10 秒超时请求（`user_data=10`）和取消请求（`user_data=11`）
4. 等待 2 个 CQE，校验取消请求返回值合理（0 / -EALREADY / -ENOENT）
5. 校验被取消超时的返回值在预期集合（`-ECANCELED` 或 `-ETIME`）

**验证点**:
- ✅ `IORING_OP_TIMEOUT` 与 `IORING_OP_TIMEOUT_REMOVE` 均可达
- ✅ `user_data` 映射正确
- ✅ 超时触发与取消语义都被验证

**预期结果**: `[PASS]` - 超时与取消机制正常

---

#### 7.4 test_linked_sqes - 链式 SQE

**描述**: 使用 `IOSQE_IO_LINK` 标志将多个操作链接为有序执行链

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建新文件
3. 准备 3 个链式 SQE：
   - SQE1: `IORING_OP_WRITE`（写入数据，设置 `IOSQE_IO_LINK`）
   - SQE2: `IORING_OP_FSYNC`（同步数据，设置 `IOSQE_IO_LINK`）
   - SQE3: `IORING_OP_READ`（读回数据，无 LINK 标志）
4. 一次性提交 3 个 SQE
5. 等待所有 3 个 CQE 完成
6. 验证读回的数据与写入的一致

**验证点**:
- ✅ `IOSQE_IO_LINK` 标志正确使用
- ✅ 链中的操作按顺序执行（write → fsync → read）
- ✅ 前一个操作完成后才执行下一个
- ✅ 数据在链式操作中正确传递

**技术要点**:
- 链式请求保证执行顺序
- 如果链中某个操作失败，后续操作也会被取消
- 适合需要严格顺序的 I/O 操作（如写后同步、写后读）

**预期结果**: `[PASS]` - 链式操作按序执行，数据一致

---

#### 7.4 test_poll_variants - JuiceFS 文件轮询能力探测

**描述**: 在 JuiceFS 挂载点上的普通文件 fd 上提交 `IORING_OP_POLL_ADD`，验证是否支持文件轮询语义

**测试步骤**:
1. 创建测试文件并以 `O_RDWR` 打开
2. 初始化 io_uring 实例
3. 对文件 fd 提交 `POLL_ADD`（请求 `POLLIN | POLLOUT`）
4. 等待 CQE 返回
5. 若返回 `-EOPNOTSUPP` / `-ENOTSUP` / `-EINVAL`，则判定为当前 JuiceFS 不支持该能力并跳过
6. 若成功，校验返回事件掩码至少包含 `POLLIN` 和 `POLLOUT`

**验证点**:
- ✅ 能准确区分 JuiceFS 是否支持文件轮询
- ✅ 支持时返回正确的 poll mask
- ✅ 不支持时能返回明确的能力结论，而不是把 eventfd 的结果误当成 JuiceFS 能力

**预期结果**: `[PASS]` - JuiceFS 支持文件轮询；或 `[SKIP]` - 当前实现不支持

---

#### 7.5 test_provide_buffers - 提供缓冲区

**描述**: 使用 `IORING_OP_PROVIDE_BUFFERS` 向 io_uring 提供缓冲区池

**测试步骤**:
1. 初始化 io_uring 实例
2. 分配 4096 字节缓冲区
3. 使用 `io_uring_prep_provide_buffers(sqe, buf, BLOCK_SIZE, 1, bgid=1, bid=0)` 提供
   - `bgid`: Buffer Group ID（缓冲区组标识）
   - `bid`: Buffer ID（缓冲区标识）
4. 提交并等待完成
5. 验证 CQE 返回成功

**验证点**:
- ✅ `IORING_OP_PROVIDE_BUFFERS` 操作正常
- ✅ 缓冲区池被正确注册
- ✅ bgid 和 bid 参数正确传递

**技术要点**:
- 提供缓冲区机制允许内核自动选择缓冲区
- 与 `IORING_OP_READ` 配合使用，通过 bgid 指定缓冲区组
- 适合网络服务器等需要动态缓冲区管理的场景

**预期结果**: `[PASS]` - 缓冲区提供机制正常

---

#### 7.6 test_iopoll - I/O 轮询模式

**描述**: 使用 `IORING_SETUP_IOPOLL` 标志初始化 io_uring，启用 I/O 轮询模式

**测试步骤**:
1. 创建 128KB 测试文件
2. 使用 `IORING_SETUP_IOPOLL` 标志初始化 io_uring 实例
3. 提交读取请求
4. 循环调用 `io_uring_wait_cqe_timeout()` 等待完成
5. 如果未完成，重新调用 `io_uring_submit()` 触发轮询
6. 验证读取成功

**验证点**:
- ✅ `IORING_SETUP_IOPOLL` 模式正常工作
- ✅ 通过轮询获取完成事件
- ✅ I/O 操作正确完成

**技术要点**:
- IOPOLL 模式下，内核不使用中断通知完成
- 应用程序需要主动调用 submit 来检查完成状态
- 减少了中断开销，适合低延迟场景
- 需要底层设备支持轮询模式

**可能结果**: `[PASS]` 或 `[SKIP]`（FUSE 文件系统可能不支持 IOPOLL）

---

#### 7.7 test_sqpoll - SQ 轮询模式

**描述**: 使用 `IORING_SETUP_SQPOLL` 标志初始化 io_uring，启用内核线程轮询提交队列

**测试步骤**:
1. 创建 128KB 测试文件
2. 使用 `IORING_SETUP_SQPOLL` 标志初始化 io_uring 实例
3. 提交读取请求
4. 使用 `io_uring_wait_cqe()` 等待完成
5. 验证 `user_data` 与读取 4096 字节

**验证点**:
- ✅ `IORING_SETUP_SQPOLL` 模式正常工作
- ✅ 内核线程自动处理提交队列
- ✅ I/O 操作正确完成

**技术要点**:
- SQPOLL 模式下，内核创建一个专用线程轮询 SQ
- 应用程序无需调用 `io_uring_submit()`
- 进一步减少了系统调用开销
- 需要 `CAP_SYS_NICE` 或 root 权限

**可能结果**: `[PASS]` 或 `[SKIP]`（需要特权，且 FUSE 可能不支持）

---

#### 7.8 test_sync_file_range - 异步文件范围同步

**描述**: 使用 `IORING_OP_SYNC_FILE_RANGE` 异步同步文件的指定范围

**测试步骤**:
1. 创建文件并写入 4096 字节数据（字符 'R'）
2. 初始化 io_uring 实例
3. 使用 `io_uring_prep_sync_file_range(sqe, fd, BLOCK_SIZE, 0, SYNC_FILE_RANGE_WRITE)` 同步
4. 提交并等待完成
5. 验证 CQE 返回成功
6. 回读文件内容，验证同步后数据可达

**验证点**:
- ✅ `IORING_OP_SYNC_FILE_RANGE` 操作正常
- ✅ 指定范围的数据被同步
- ✅ `SYNC_FILE_RANGE_WRITE` 标志正确使用
- ✅ 同步后文件可读且数据一致

**sync_file_range vs fsync 区别**:
- `fsync`: 同步整个文件（数据 + 元数据）
- `sync_file_range`: 仅同步指定偏移和长度的数据
- `sync_file_range` 更灵活，适合大文件的部分同步

**预期结果**: `[PASS]` - 异步范围同步成功

---

#### 7.9 test_epoll_ctl - 异步 epoll 控制

**描述**: 使用 `IORING_OP_EPOLL_CTL` 通过 io_uring 异步管理 epoll 实例

**测试步骤**:
1. 初始化 io_uring 实例
2. 创建 epoll 实例（`epoll_create1()`）
3. 创建管道
4. 使用 `io_uring_prep_epoll_ctl(sqe, epfd, pfd[0], EPOLL_CTL_ADD, &ev)` 添加监听
5. 提交并等待完成
6. 向管道写入数据并通过 `epoll_wait()` 验证事件实际可达

**验证点**:
- ✅ `IORING_OP_EPOLL_CTL` 操作正常
- ✅ 管道读端被添加到 epoll 实例
- ✅ 可以通过 io_uring 管理 epoll
- ✅ 注册后事件链路真实生效（非仅返回码）

**技术要点**:
- 允许将 epoll 操作集成到 io_uring 的提交流程中
- 支持的操作：EPOLL_CTL_ADD、EPOLL_CTL_MOD、EPOLL_CTL_DEL
- 适合需要同时管理 io_uring 和 epoll 的场景

**预期结果**: `[PASS]` - 异步 epoll 控制成功

---

## 测试覆盖矩阵

| 功能特性 | basic_io | fixed_buffers | registered_files | splice | file_ops | dir_ops | advanced |
|---------|----------|---------------|------------------|--------|----------|---------|----------|
| IORING_OP_READ | ✅ | - | ✅ | - | - | - | ✅ |
| IORING_OP_WRITE | ✅ | - | ✅ | - | - | - | ✅ |
| IORING_OP_READV | ✅ | - | - | - | - | - | - |
| IORING_OP_WRITEV | ✅ | - | - | - | - | - | - |
| IORING_OP_READ_FIXED | - | ✅ | - | - | - | - | - |
| IORING_OP_WRITE_FIXED | - | ✅ | - | - | - | - | - |
| IORING_OP_OPENAT | - | - | - | - | ✅ | - | - |
| IORING_OP_OPENAT2 | - | - | - | - | ✅ | - | - |
| IORING_OP_CLOSE | - | - | - | - | ✅ | - | - |
| IORING_OP_STATX | - | - | - | - | ✅ | - | - |
| IORING_OP_FSYNC | - | - | - | - | ✅ | - | ✅ |
| IORING_OP_FALLOCATE | - | - | - | - | ✅ | - | - |
| IORING_OP_MKDIRAT | - | - | - | - | - | ✅ | - |
| IORING_OP_UNLINKAT | - | - | - | - | - | ✅ | - |
| IORING_OP_RENAMEAT | - | - | - | - | - | ✅ | - |
| IORING_OP_LINKAT | - | - | - | - | - | ✅ | - |
| IORING_OP_SYMLINKAT | - | - | - | - | - | ✅ | - |
| IORING_OP_SPLICE | - | - | - | ✅ | - | - | - |
| IORING_OP_TEE | - | - | - | ✅ | - | - | - |
| IORING_OP_NOP | - | - | - | - | - | - | ✅ |
| IORING_OP_TIMEOUT | - | - | - | - | - | - | ✅ |
| IORING_OP_TIMEOUT_REMOVE | - | - | - | - | - | - | ✅ |
| IORING_OP_POLL_ADD | - | - | - | - | - | - | ✅ |
| IORING_OP_PROVIDE_BUFFERS | - | - | - | - | - | - | ✅ |
| IORING_OP_SYNC_FILE_RANGE | - | - | - | - | - | - | ✅ |
| IORING_OP_EPOLL_CTL | - | - | - | - | - | - | ✅ |
| 注册缓冲区 | - | ✅ | - | - | - | - | - |
| 注册文件 | - | - | ✅ | - | - | - | - |
| IOSQE_FIXED_FILE | - | - | ✅ | - | - | - | - |
| IOSQE_IO_LINK | - | - | - | - | - | - | ✅ |
| IORING_SETUP_IOPOLL | - | - | - | - | - | - | ✅ |
| IORING_SETUP_SQPOLL | - | - | - | - | - | - | ✅ |
| 批量 I/O | ✅ | ✅ | - | - | - | - | - |
| 读写一致性 | ✅ | ✅ | ✅ | - | - | - | - |

**总计**: 39 个测试用例覆盖 34 个功能维度

## 常见问题

### Q: 为什么有些测试显示 [SKIP]？

A: 这不是错误。当内核版本或文件系统不支持某个特定功能时，测试会优雅地跳过。常见原因：
- 内核版本过低（如 IOPOLL 需要 5.1+，SQPOLL 需要特权）
- FUSE 文件系统不支持某些操作（如 IOPOLL）
- 缺少必要的权限（如 SQPOLL 需要 CAP_SYS_NICE）

### Q: 如何判断测试是否全部通过？

A: 查看每个测试程序的 Summary 输出，确认 FAIL 数量为 0。run_tests.sh 会运行所有测试程序并汇总结果。

### Q: 测试失败的可能原因有哪些？

1. io_uring 被内核禁用（检查 `/proc/sys/kernel/io_uring_disabled`）
2. 内核版本过低（建议 5.6+）
3. liburing 库版本与内核不匹配
4. JuiceFS 挂载选项不正确
5. 权限不足（某些操作需要 root 或 CAP_SYS_NICE）
6. FUSE 文件系统对某些 io_uring 操作的支持有限

### Q: IOPOLL 和 SQPOLL 有什么区别？

| 特性 | IOPOLL | SQPOLL |
|------|--------|--------|
| 轮询对象 | 完成队列 (CQ) | 提交队列 (SQ) |
| 目的 | 减少中断开销 | 减少系统调用开销 |
| 应用程序行为 | 需主动调用 submit 轮询完成 | 内核线程自动处理提交 |
| 适用场景 | 低延迟块设备 | 高频 I/O 提交 |
| 权限要求 | 无特殊 | CAP_SYS_NICE 或 root |

### Q: 可以单独运行某个测试吗？

A: 可以。每个测试程序都可以独立运行，只需传入测试目录路径即可。

## 技术参考

### io_uring 核心数据结构

```c
struct io_uring_sqe {
    __u8  opcode;       // 操作码（如 IORING_OP_READ）
    __u8  flags;        // SQE 标志（如 IOSQE_FIXED_FILE）
    __u16 ioprio;       // I/O 优先级
    __s32 fd;           // 文件描述符或注册文件索引
    __u64 off;          // 偏移量
    __u64 addr;         // 缓冲区地址或 iovec 指针
    __u32 len;          // 长度
    union { ... };      // 操作特定字段
    __u64 user_data;    // 用户数据（在 CQE 中返回）
    ...
};

struct io_uring_cqe {
    __u64 user_data;    // 对应 SQE 的 user_data
    __s32 res;          // 操作结果（字节数或错误码）
    __u32 flags;        // CQE 标志
};
```

### 内核版本要求

| 功能 | 最低内核版本 |
|------|------------|
| io_uring 基础 | 5.1 |
| IORING_OP_OPENAT/CLOSE | 5.5 |
| IORING_OP_STATX | 5.5 |
| IORING_OP_READ_FIXED/WRITE_FIXED | 5.1 |
| IORING_OP_SPLICE | 5.5 |
| IORING_OP_TEE | 5.5 |
| IORING_OP_OPENAT2 | 5.6 |
| IORING_OP_FALLOCATE | 5.6 |
| IORING_OP_MKDIRAT | 5.6 |
| IORING_OP_UNLINKAT | 5.6 |
| IORING_OP_RENAMEAT | 5.6 |
| IORING_OP_LINKAT | 5.6 |
| IORING_OP_SYMLINKAT | 5.6 |
| IORING_OP_PROVIDE_BUFFERS | 5.7 |
| IORING_OP_EPOLL_CTL | 5.6 |
| IORING_OP_SYNC_FILE_RANGE | 5.6 |
| IORING_SETUP_IOPOLL | 5.1 |
| IORING_SETUP_SQPOLL | 5.1 |
| IOSQE_IO_LINK | 5.3 |

## 许可证

遵循 JuiceFS 主项目的开源许可证。
