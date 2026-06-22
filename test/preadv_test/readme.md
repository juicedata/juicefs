         
## JuiceFS preadv/pwritev 测试结果总结

**测试环境**：JuiceFS 挂载点 `/tmp/jfs`，内核 6.8.0-55-generic

### 总览：12 PASS / 0 FAIL / 1 SKIP

| 测试套件 | 结果 | 详情 |
|---------|------|------|
| **Basic preadv/pwritev** | ✅ 8/8 | 全部通过 |
| **preadv2/pwritev2 Flags** | ✅ 1/2 | 1 项 SKIP |
| **O_DIRECT + preadv/pwritev** | ✅ 3/3 | 全部通过 |

---

### 支持情况详细说明

1. **首先**

    虽然内核 fuse 请求里并没有 `scatter-gatter`（分散聚集）接口，但 `preadv/pwritev` 可以正常调用，不会报错。内核会把 `preadv/pwritev` 拆分成独立的 `read/write` 请求发送到 juicefs，虽然无法完全享受向量化的优化收益，但相比应用层多次调用 `write/read` 仍会有一定的性能提升（主要是因为减少了系统调用次数）。

2. **基础能力**

    `preadv/pwritev` 的基础语义已在 `test_basic.c` 中覆盖并通过，主要验证：

    - 多 `iovec` 读写的长度与数据一致性
    - 指定 `offset` 的读写正确性
    - 边界行为（部分读取、EOF 返回 `0`）
    - 特殊 `iovec` 场景（如 `0` 长度项）

3. **高级 flag 支持如下**

    - `RWF_APPEND` 支持（由内核 vfs 处理）
    - `RWF_NOWAIT` 不支持（`FUSE` 协议目前没有处理该类型的 flag）
    - `RWF_HIPRI` 不支持（依赖于 `poll` 接口，`FUSE` 目前不支持此接口）
    - `RWF_SYNC/RWF_DSYNC` 不支持（`Juicefs` 暂不支持 `preadv/pwritev` 中携带这些 flag，可使用 `fsync` 等接口替代）

    注：上述不支持的 `flag` 在代码中依然可以调用，不会报错，只是不会产生实际作用。

4. **`O_DIRECT` 测试说明**

    当前用例覆盖了 3 类场景：

    - 对齐缓冲区下的 `O_DIRECT + preadv`（验证可正常读并做数据一致性校验）
    - 对齐缓冲区下的 `O_DIRECT + pwritev`（验证可正常写并做数据一致性校验）
    - 非对齐缓冲区下的 `O_DIRECT + preadv`（能力探测：不同内核/文件系统可能返回 `EINVAL`，也可能被接受）

    注：第三项属于平台相关行为探测，不用于断言 `JuiceFS` 在所有环境下必须拒绝非对齐缓冲区。

### 测试代码位置

```
test/preadv_test/
├── common.h          # 公共头文件
├── test_basic.c      # 基础功能测试（当前 8 项）
├── test_flags.c      # preadv2/pwritev2 flags 测试（当前 2 项）
├── test_odirect.c    # O_DIRECT 测试（当前 3 项）
├── Makefile
└── preadv_test.sh
```