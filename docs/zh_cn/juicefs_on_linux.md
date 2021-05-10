# Linux 系统使用 JuiceFS

[快速上手指南](quick_start_guide.md) 中介绍了 JuiceFS 在 Linux 系统的中的使用方法。

## 编译安装 JuiceFS 客户端

### 1. 依赖的软件包

使用源代码手动编译 JuiceFS 客户端，你需要先安装以下工具：

- [Go](https://golang.org/) 1.14+
- GCC 5.4+

### 2. 手动编译

克隆仓库到本地：

```shell
$ git clone https://github.com/juicedata/juicefs.git
```

进入 juicefs 目录：

```shell
$ cd juicefs
```

执行编译：

```shell
$ make
```

> **提示**：中国地区用户，可以设置  `GOPROXY` 加快 Go 模块的下载速度，例如： [Goproxy China](https://github.com/goproxy/goproxy.cn)。

## 常见错误

```
./juicefs: /lib/x86_64-linux-gnu/libc.so.6: version `GLIBC_2.32' not found (required by ./juicefs)
./juicefs: /lib/x86_64-linux-gnu/libc.so.6: version `GLIBC_2.33' not found (required by ./juicefs)
```

