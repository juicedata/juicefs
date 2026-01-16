1. juicefs 目前支持的quota类型
文件系统总配额、子目录配额、用户和用户组配额。可同时限制可用容量和可用inode数

2. 文件系统总配额是如何实现的，如何更新的

在format结构体中记录文件系统总配额，这些值在juicefs format和juicefs config --quota等命令里配置
挂载后被baseMeta读到，并且配额全局计数器 usedSpace / usedInodes 来实现配额控制，核心逻辑在quota.go和 base.go

1. 总配额是怎么“存”和“查”的？
1）计数器存放位置
在 baseMeta 里有一组全局计数（fsStat）：
usedSpace / usedInodes：当前已用空间 & 已用 inode（持久化在 Redis / SQL / TKV 等后端的 counter 里）
newSpace / newInodes：本节点最近一段时间内的增量（在内存累积，定期 flush，到后端累加到 usedSpace/usedInodes）
statfs 时会调用（节选逻辑在 statRootFs）：
先从本地 usedSpace/usedInodes 为基准
若配置 FastStatfs=false 或本地是未知值，会去元数据后端调用 en.getCounter(usedSpace/totalInodes) 同步最新值
然后把 newSpace/newInodes 加进去，得到当前实际已用量
如果 format.Capacity > 0，totalspace = Capacity，avail = Capacity - used
如果没设置 Capacity，则动态估算一个“伪总容量”返回
根目录通过 File.GetQuota 还能直接返回一个 Quota 结构，里边 MaxSpace = format.Capacity、MaxInodes = format.Inodes（只是为了展示配置的总配额）。
2. 写入文件时：如何检查 & 更新总配额？
2.1 写入前的配额检查
所有会增加空间 / inode 的操作（write/truncate/fallocate/mknod/link 等），在真正执行前都会算出本次操作对应的增量：
space：按 4K 对齐后的增量（align4K），即底层块实际占用空间的变化
inodes：新建文件/目录时 +1，删除/落到 0 链接数时 -1
然后统一走 baseMeta.checkQuota（quota.go）：
func (m *baseMeta) checkQuota(ctx Context, space, inodes int64, uid, gid uint32, parents ...Ino) syscall.Errno {    // 1) user quota    // 2) group quota    // 3) 文件系统总配额（format.Capacity / format.Inodes）    // 4) 目录/父目录配额（dir quota）}
其中检查总配额的部分是：
若 space > 0 && format.Capacity > 0，判断
usedSpace
+
n
e
w
S
p
a
c
e
+
s
p
a
c
e
>
C
a
p
a
c
i
t
y
usedSpace+newSpace+space>Capacity
超了就返回 ENOSPC（No space left on device）
若 inodes > 0 && format.Inodes > 0，判断
usedInodes
+
n
e
w
I
n
o
d
e
s
+
i
n
o
d
e
s
>
I
n
o
d
e
s
usedInodes+newInodes+inodes>Inodes
超了也返回 ENOSPC
如果没超，总配额角度允许写入，接着还会看：
用户配额 / 组配额（checkUserQuota/checkGroupQuota）
父目录链路上的目录配额（checkDirQuota）
任何一项超限都会返回 EDQUOT（磁盘配额超限）。
2.2 写入成功后的统计更新
写入/扩展成功后，会把本次的 space/inodes 增量更新到：
卷级统计：
通过 en.updateStats(space, inodes) 累加到 fsStat.newSpace/newInodes
后台 goroutine 周期性 doFlushStats，把 newSpace/newInodes 落到后端 counter（usedSpace/usedInodes）
目录统计 & 目录配额：
updateParentStat 调用 updateDirStat，把 length/space/inodes 累加到内存中的 dirStats[ino]
同时 updateDirQuota 会对这棵目录链路上的所有含配额的 inode 调用 Quota.update(space, inodes)，累加到 newSpace/newInodes
周期性 flushDirStat 和 flushQuotas 会把这些增量批量写回后端（doUpdateDirStat / doFlushQuotas）
用户 / 组配额（如果启用）：
updateUserGroupQuota 对对应 uid/gid 的 Quota 结构加 newSpace/newInodes
也由 flushQuotas 周期性落盘并累加到 UsedSpace/UsedInodes
因此：总配额的使用量并不是每次写入都立刻写后端，而是先在内存做“新增长度计数”，再由后台定时器批量 flush。
3. 删除文件 / 释放空间时：如何更新？
删除相关的操作（unlink/rmdir/batchUnlink/truncate 缩短/fallocate punch hole 等）会做相反的事情：
在引擎层计算负增量
这些函数的签名里都有 delta *dirStat 或类似结构（见 engine 接口里的 doUnlink/doBatchUnlink/doTruncate/doFallocate 等），由具体后端实现算出：
delta.space：这次操作实际释放的块大小（按 4K 对齐，多硬链接时只在 nlink 降到 0 时真正扣空间）
delta.inodes：真正被删除的 inode 数
统一走统计 / 配额更新
上层 baseMeta 拿到这些 delta 后，直接调用与写入相同的更新函数，只不过 space/inodes 是负数：
en.updateStats(delta.space, delta.inodes) → 全局 newSpace/newInodes 变为负数，flush 后把 usedSpace/usedInodes 减小
updateDirStat / updateDirQuota → 目录统计 / 目录配额的 UsedSpace/UsedInodes 同样减少
updateUserGroupQuota → 用户 / 组配额也同步减少
注意点：
目录配额统计时，根目录本身通常会被特殊处理（例如 align4K(0) 部分被忽略），这在 initializeQuotaUsage / handleQuotaCheck 里可以看到：计算 UsedSpace 会减去根目录本身的空间。
有硬链接的文件只在最后一个链接删除时算一次空间释放，避免重复扣减。
4. 小结：从“写/删”到“总配额”的整个链路
写入前：
先根据本次操作估算 
Δ
s
p
a
c
e
/
Δ
i
n
o
d
e
s
Δspace/Δinodes
checkQuota：
用户配额 / 组配额
卷级总配额（format.Capacity/Inodes + 全局计数器）
目录链路上的目录配额
任一超限 → 返回 EDQUOT 或 ENOSPC，写入被拒绝
写入 / 删除成功后：
把这次的 
Δ
s
p
a
c
e
/
Δ
i
n
o
d
e
s
Δspace/Δinodes（可能是负数）：
记到全局 newSpace/newInodes（卷级统计）
记到相关目录的 dirStats 和目录配额 Quota.newSpace/newInodes
如开启用户 / 组配额，也记到对应 uid/gid 的 Quota 结构里
后台 goroutine 周期性 flush，把 neue 计数归并到持久化的 UsedSpace/UsedInodes / 目录统计表 / 用户组配额表