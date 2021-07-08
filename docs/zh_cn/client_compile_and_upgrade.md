# JuiceFS 编译安装和升级

## 从源代码手动编译

如果你想优先体验 JuiceFS 的新功能，可以从我们仓库的 main 分支克隆代码，手动编译最新的客户端。

### 克隆源码

```shell
$ git clone https://github.com/juicedata/juicefs.git
```

### 执行编译

JuiceFS 客户端使用 Go 语言开发，因此在编译之前，你提前在本地安装好依赖的工具：

- [Go](https://golang.org) 1.15+
- GCC 5.4+

> **提示**：对于中国地区用户，为了加快获取 Go 模块的速度，建议通过 `GOPROXY` 环境变量设置国内的镜像服务器。例如：[Goproxy China](https://github.com/goproxy/goproxy.cn)。

进入源代码目录：

```shell
$ cd juicefs
```

开始编译：

```shell
$ make
```

编译成功以后，可以在当前目录中找到编译好的 `juicefs` 二进制程序。

## JuiceFS 客户端升级

JuiceFS 客户端是一个名为 `juicefs` 二进制文件，升级时只需使用新版二进制文件替换旧版即可。
