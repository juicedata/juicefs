         
## JuiceFS preadv/pwritev 测试结果总结

**测试环境**：JuiceFS 1.4-beta2，内核 6.8.0-55-generic

### 总览：12 PASS / 0 FAIL / 1 SKIP

| 测试套件 | 结果 | 详情 |
|---------|------|------|
| **Basic preadv/pwritev** | ✅ 8/8 | 全部通过 |
| **preadv2/pwritev2 Flags** | ✅ 1/2 | 1 项 SKIP |
| **O_DIRECT + preadv/pwritev** | ✅ 3/3 | 全部通过 |

---

### 支持情况详细说明

1. **首先**

    内核vfs会对 `preadv/pwritev` 接口进行预处理，把 多个 `iovec` 进行合并然后以普通读写接口的形式发送到juicefs，应用可以享受到系统调用次数减少和用户态内核态上下文切换次数减少带来的性能优化

2. **基础能力**

    `preadv/pwritev` 的基础语义已在 `test_basic.c` 中覆盖并通过，主要验证：

    - 多 `iovec` 读写的长度与数据一致性
    - 指定 `offset` 的读写正确性
    - 边界行为（部分读取、EOF 返回 `0`）
    - 特殊 `iovec` 场景（如 `0` 长度项）

3. **高级 flag 支持如下**

    - `RWF_APPEND` 完全支持
    - `RWF_NOWAIT` 明确不支持，会报错 `EOPNOTSUPP`.
    - `RWF_HIPRI` 可以调用，但没有任何实际效果（高优先级轮询依赖于 `iopoll` 接口，`FUSE` 目前不支持此接口，依然是走普通 io 路径）
    - `RWF_SYNC/RWF_DSYNC` 部分支持（内核默认会处理掉这两个 flag，在 `write` 以后 发送一次 `fsync` 请求到 JuiceFS， JuiceFS 并没有区分两者，统一当作 `sync` 方式来处理）

4. **`O_DIRECT` 测试说明**

    当前用例覆盖了 buffer io 和 direct io，其中 direct io 包括以下场景：

    - 对齐缓冲区下的 `O_DIRECT + preadv`（验证可正常读并做数据一致性校验）
    - 对齐缓冲区下的 `O_DIRECT + pwritev`（验证可正常写并做数据一致性校验）

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