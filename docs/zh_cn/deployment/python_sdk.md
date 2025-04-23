---
title: Python SDK
sidebar_position: 6
---

JuiceFS 社区版从 v1.3.0 引入 Python SDK，适合无法使用 FUSE 挂载的容器化或虚拟化环境使用。并且 Python SDK 实现了 fsspec 的接口规范，可方便的对接 Ray 等框架。

## 编译

你可以在当前工作环境中直接编译 Python SDK，也可以使用 Docker 容器进行编译。两种方式都需要先克隆仓库并进入 SDK 所在目录。

```bash
# 克隆 JuiceFS 仓库
git clone https://github.com/juicedata/juicefs.git
# 进入 JuiceFS 目录
cd juicefs/sdk/python
```

### 直接编译

直接编译需要 `go1.20+` 和 `python3` 环境。

#### 第一步：编译 libjfs.so

```bash
go build -buildmode c-shared -ldflags="-s -w" -o juicefs/juicefs/libjfs.so ../java/libjfs
```

编译产生的 `libjfs.so` 和 `libjfs.h` 文件在 `sdk/python/juicefs/juicefs` 目录下。

#### 第二步：编译 Python SDK

```bash
cd juicefs && python3 -m build -w
```

编译好的 Python SDK 会在 `juicefs/sdk/python/dist` 目录下，文件名为 `juicefs-1.3.0-py3-none-any.whl`。

### Docker 编译

使用 Docker 容器编译需要当前系统安装了 `Docker`、`make` 和 `go1.20+` 环境。

#### 第一步：构建 Docker 镜像

```bash
# For arm64
make arm-builder

# For amd64
make builder
```

#### 第二步：编译 Python SDK

```bash
make juicefs
```

编译好的 Python SDK 会在 `juicefs/sdk/python/dist` 目录下，文件名为 `juicefs-1.3.0-py3-none-any.whl`。

### 编译报错处理

如果在编译时遇到 `sed: 1: "juicefs/setup.py": invalid command code j` 的错误，可以尝试将 `Makefile` 中 `sed` 相关的命令注释掉。

## 安装与使用

### 安装 SDK

将编译好的 `juicefs-1.3.0-py3-none-any.whl` 文件拷贝到目标机器上，使用 `pip` 安装：

```bash
pip install juicefs-1.3.0-py3-none-any.whl
```

### 准备文件系统

:::tip
JuiceFS 的 Python SDK 暂不支持格式化文件系统，因此在使用之前请确保已经预先创建了 JuiceFS 文件系统。
:::

假设这里已经有一个预先创建好的名称为 `myfs` 的文件系统，元数据引擎 URL 为 `redis://192.168.1.8/0`。

### 使用 Client

`Client` 类的实现与 Python 的 io 模块类似。

可以使用以下代码实例化一个 JuiceFS 客户端，`name` 参数为文件系统名称，`meta` 参数为元数据引擎的 URL。其中，`name`参数必须存在，但允许使用空字符串或 `None`。

```python
from juicefs import Client

# 创建 JuiceFS 客户端
jfs = Client(name='', meta='redis://192.168.1.8/0')

# 列出目录中的文件
jfs.listdir('/')
```

### 使用 fsspec

JuiceFS 的 Python SDK 还支持 `fsspec` 接口来操作 JuiceFS 文件系统。

```bash
# 安装 fsspec
pip install fsspec
```

`fsspec` 的使用方式与 `Client` 类类似，只是需要指定 `jfs` 或 `juicefs` 作为文件系统类型。

```python
import fsspec
from juicefs.spec import JuiceFS

jfs = fsspec.filesystem('jfs', name='', meta='redis://192.168.1.8/0')

# 列出目录中的文件
jfs.ls('/')
```

### 获取帮助信息

可以使用 `help()` 函数获取类和方法的帮助信息。

```python
import juicefs

help(juicefs.Client)
```

也可以使用 `dir()` 函数获取类和方法的列表。

```python
import juicefs

dir(juicefs.Client)
```
