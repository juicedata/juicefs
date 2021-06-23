# POSIX 兼容性

JuiceFS 通过了 [pjdfstest](https://github.com/pjd/pjdfstest) 最新的 8813 项测试。

```
All tests successful.

Test Summary Report
-------------------
/root/soft/pjdfstest/tests/chown/00.t          (Wstat: 0 Tests: 1323 Failed: 0)
  TODO passed:   693, 697, 708-709, 714-715, 729, 733
Files=235, Tests=8813, 233 wallclock secs ( 2.77 usr  0.38 sys +  2.57 cusr  3.93 csys =  9.65 CPU)
Result: PASS
```

此外，JuiceFS 还提供：

- 关闭再打开（close-to-open）一致性。一旦一个文件写入完成并关闭，之后的打开和读操作保证可以访问之前写入的数据。如果是在同一个挂载点，所有写入的数据都可以立即读。
- 重命名以及所有其他元数据操作都是原子的，由 Redis 的事务机制保证。
- 当文件被删除后，同一个挂载点上如果已经打开了，文件还可以继续访问。
- 支持 mmap
- 支持 fallocate 以及空洞
- 支持扩展属性
- 支持 BSD 锁（flock）
- 支持 POSIX 记录锁（fcntl）
