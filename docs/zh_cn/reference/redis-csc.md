# Redis 客户端侧缓存支持

从 6.0 版本开始，Redis 提供了[客户端侧缓存](https://redis.io/docs/latest/develop/reference/client-side-caching)功能，允许客户端以更快、更高效的方式在本地维护数据缓存。JuiceFS 完整支持该功能，可为元数据操作带来显著的性能提升。

## 工作原理

Redis 客户端侧缓存（CSC）的工作方式如下：

1. 客户端通过 `CLIENT TRACKING ON BCAST` 启用追踪模式
2. 客户端从 Redis 读取数据后在本地进行缓存
3. 当任意客户端修改了被缓存的键时，Redis 会通知该客户端
4. 客户端将这些键从本地缓存中失效

这将减少网络流量、降低延迟并提高吞吐量。

## 配置

JuiceFS 通过元数据 URL 中的以下选项支持 Redis CSC：

```shell
--meta-url="redis://localhost/1?client-cache=true" # 启用客户端侧缓存（始终使用 BCAST 模式）
--meta-url="redis://localhost/1?client-cache=true&client-cache-size=500" # 设置缓存大小（默认 12800）
--meta-url="redis://localhost/1?client-cache=true&client-cache-expire=60s" # 设置缓存过期时间（默认：60s）
```

### 选项

- `client-cache`：以 BCAST 模式启用客户端侧缓存（设置为除 "false" 以外的任意值）
- `client-cache-size`：最大缓存大小（默认：12800）
- `client-cache-expire`：缓存过期时间（默认：60s）
- `client-cache-preload`：挂载后预加载根目录下文件对象的数量（默认：0）

启用客户端侧缓存后，JuiceFS 会缓存：

1. **Inode 属性**：文件/目录的元数据，如权限、大小、时间戳
2. **目录项**：名称到 inode 的映射，用于加速查找

> **注意：** Redis 客户端侧缓存需要 Redis 服务端版本 6.0 或更高。在旧版本 Redis 上使用该功能将导致错误。

### 预加载缓存

当启用客户端侧缓存且设置了 `client-cache-preload` 时，JuiceFS 会在挂载后预加载根目录下文件对象的属性和目录项。这种懒惰预加载在后台进行，有助于：

1. 为常见操作预热缓存
2. 降低初始文件系统操作的延迟
3. 从文件系统挂载的那一刻起就提供更好的性能

预加载过程通过以下方式智能地优先处理最重要的 inode：

1. 从根目录开始
2. 加载访问最频繁的顶层目录和文件
3. 递归探索重要的子目录

预加载过程在后台 goroutine 中运行，具有故障安全机制，不会阻塞或影响正常的文件系统操作。

## 模式

JuiceFS 使用 BCAST 模式以保证简洁性和可靠性：

- **BCAST 模式**：客户端访问的所有键都会被追踪，任何更改都会发送通知。

BCAST 模式提供了最简单的实现，同时确保所有客户端之间的缓存一致性。

## 要求

- Redis 服务端版本 6.0 或更高
- 启用了 CSC 支持的 JuiceFS

## 性能考量

1. 默认的 12800 缓存大小对于大多数工作负载已足够（attr 和 entry 各缓存 12800 个）
2. 对于拥有数百万文件的超大型文件系统，增大缓存大小可能有所帮助
3. 缓存对于具有大量重复操作的元数据密集型工作负载最为有效
4. 对于写入非常频繁的工作负载，建议禁用 CSC，因为失效流量可能抵消其带来的收益

## 故障排查

如果在启用 CSC 的情况下遇到崩溃或不稳定问题：

1. 更新到最新版本的 JuiceFS，其中包含针对 CSC 的重要修复
2. 尝试通过 `client-cache-size` 减小缓存大小
3. 检查 Redis 服务端日志中是否存在内存或客户端追踪问题
4. 确保 Redis 服务端版本为 6.0 或更高
5. 如果问题持续存在，通过移除 `client-cache` 参数来禁用 CSC

JuiceFS 为各种 Redis CSC 特定响应提供了健壮的错误处理，以确保在 Redis 因客户端追踪而发送意外响应格式时仍能稳定运行。

## 参考资料

- [Redis 客户端侧缓存文档](https://redis.io/docs/latest/develop/reference/client-side-caching)
