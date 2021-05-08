# JuiceFS 客户端安装升级

### 下载安装

You can download precompiled binaries from [releases page](https://github.com/juicedata/juicefs/releases).

### 手动编译安装

You need first installing [Go](https://golang.org/) 1.14+ and GCC 5.4+, then run following commands:

```
$ git clone https://github.com/juicedata/juicefs.git
$ cd juicefs
$ make
```

For users in China, it's recommended to set `GOPROXY` to speed up compilation, e.g. [Goproxy China](https://github.com/goproxy/goproxy.cn).

### Dependency

A Redis server (>= 2.8) is needed for metadata, please follow [Redis Quick Start](https://redis.io/topics/quickstart).

[macFUSE](https://osxfuse.github.io/) is also needed for macOS.

The last one you need is object storage. There are many options for object storage, local disk is the easiest one to get started.

## JuiceFS 客户端升级

JuiceFS 客户端只有一个二进制文件，升级时只需用新版本替换旧版本的二进制文件即可。