# 启用 JuiceFS 的 S3 网关

JuiceFS 从 v0.11 开始引入了 S3 网关，这是一个通过 [MinIO S3 网关](https://docs.min.io/docs/minio-gateway-for-s3.html)实现的功能。它为 JuiceFS 中的文件提供跟 S3 兼容的 RESTful API，在不方便挂载的情况下能够用 s3cmd、AWS CLI、MinIO Client（mc）等工具管理 JuiceFS 上存储的文件。另外，S3 网关还提供了一个基于网页的文件管理器，用户使用浏览器就能对 JuiceFS 上的文件进行常规的增删管理。

因为 JuiceFS 会将文件分块存储到底层的对象存储中，不能直接使用底层对象存储的接口和界面来直接访问文件，S3 网关提供了类似底层对象存储的访问能力，架构图如下：

![](images/juicefs-s3-gateway-arch.png)

## 先决条件

S3 网关是建立在 JuiceFS 文件系统之上的功能，如果你还没有 JuiceFS 文件系统，请先参考 [快速上手指南](quick_start_guide.md) 创建一个。

JuiceFS S3 网关是 v0.11 中引入的功能，请确保您拥有最新版本的 JuiceFS。

## 快速开始

使用 JuiceFS 的 `gateway` 子命令即可在当前主机启用 S3 网关。在开启功能之前，需要先设置 `MINIO_ROOT_USER` 和 `MINIO_ROOT_PASSWORD` 两个环境变量，即访问 S3 API 时认证身份用的 Access Key 和 Secret Key。可以简单的把它们视为 S3 网关的用户名和密码。例如：

```shell
$ export MINIO_ROOT_USER=admin
$ export MINIO_ROOT_PASSWORD=12345678
$ juicefs gateway redis://localhost:6379 localhost:9000
```

以上三条命令中，前两条命令用于设置环境变量。注意，`MINIO_ROOT_USER` 的长度至少 3 个字符， `MINIO_ROOT_PASSWORD` 的长度至少 8 个字符（Windows 用户请改用 `set` 命令设置环境变量，例如：`set MINIO_ROOT_USER=admin`）。

最后一条命令用于启用 S3 网关，`gateway` 子命令至少需要提供两个参数，第一个是存储元数据的数据库 URL，第二个是 S3 网关监听的地址和端口。你可以根据需要在 `gateway` 子命令中添加[其他选项](command_reference.md#juicefs-gateway)优化 S3 网关，比如，可以将默认的本地缓存设置为 20 GiB。

```shell
$ juicefs gateway --cache-size 20480 redis://localhost:6379 localhost:9000
```

在这个例子中，我们假设 JuiceFS 文件系统使用的是本地的 Redis 数据库。当 S3 网关启用时，在**当前主机**上可以使用 `http://localhost:9000` 这个地址访问到 S3 网关的管理界面。

![](images/s3-gateway-file-manager.jpg)

如果你希望通过局域网或互联网上的其他主机访问 S3 网关，则需要调整监听地址，例如：

```shell
$ juicefs gateway redis://localhost:6379 0.0.0.0:9000
```

这样一来，S3 网关将会默认接受所有网络请求。不同的位置的 S3 客户端可以使用不同的地址访问 S3 网关，例如：

- S3 网关所在主机中的第三方客户端可以使用 `http://127.0.0.1:9000` 或 `http://localhost:9000` 进行访问；
- 与 S3 网关所在主机处于同一局域网的第三方客户端可以使用 `http://192.168.1.8:9000` 访问（假设启用 S3 网关的主机内网 IP 地址为 192.168.1.8）；
- 通过互联网访问 S3 网关可以使用 `http://110.220.110.220:9000` 访问（假设启用 S3 网关的主机公网 IP 地址为 110.220.110.220）。

## 访问 S3 网关

各类支持 S3 API 的客户端、桌面程序、Web 程序等都可以访问 JuiceFS S3 网关。使用时请注意 S3 网关监听的地址和端口。

> **提示**：以下示例均为使用第三方客户端访问本地主机上运行的 S3 网关。在具体场景下，请根据实际情况调整访问 S3 网关的地址。

### 使用 AWS CLI

从 [https://aws.amazon.com/cli](https://aws.amazon.com/cli) 下载并安装 AWS CLI，然后进行配置：

```bash
$ aws configure
AWS Access Key ID [None]: admin
AWS Secret Access Key [None]: 12345678
Default region name [None]:
Default output format [None]:
```

程序会通过交互式的方式引导你完成新配置的添加，其中 `Access Key ID` 与 `MINIO_ROOT_USER` 相同，`Secret Access Key` 与 `MINIO_ROOT_PASSWORD` 相同，区域名称和输出格式请留空。

之后，即可使用 `aws s3` 命令访问 JuiceFS 存储，例如：

```bash
# List buckets
$ aws --endpoint-url http://localhost:9000 s3 ls

# List objects in bucket
$ aws --endpoint-url http://localhost:9000 s3 ls s3://<bucket>
```

### 使用 MinIO 客户端

首先参照 [MinIO 下载页面](https://min.io/download)安装 mc，然后添加一个新的 alias：

```bash
$ mc alias set juicefs http://localhost:9000 admin 12345678 --api S3v4
```

依照 mc 的命令格式，以上命令创建了一个别名为 `juicefs` 的配置。特别注意，命令中必须指定 API 版本，即 `--api "s3v4"`。

然后，你可以通过 mc 客户端自由的在本地磁盘与 JuiceFS 存储以及其他云存储之间进行文件和文件夹的复制、移动、增删等管理操作。

```shell
$ mc ls juicefs/jfs
[2021-10-20 11:59:00 CST] 130KiB avatar-2191932_1920.png
[2021-10-20 11:59:00 CST] 4.9KiB box-1297327.svg
[2021-10-20 11:59:00 CST]  21KiB cloud-4273197.svg
[2021-10-20 11:59:05 CST]  17KiB hero.svg
[2021-10-20 11:59:06 CST] 1.7MiB hugo-rocha-qFpnvZ_j9HU-unsplash.jpg
[2021-10-20 11:59:06 CST]  16KiB man-1352025.svg
[2021-10-20 11:59:06 CST] 1.3MiB man-1459246.ai
[2021-10-20 11:59:08 CST]  19KiB sign-up-accent-left.07ab168.svg
[2021-10-20 11:59:10 CST]  11MiB work-4997565.svg
```

## 监控指标收集

> **注意**：该特性需要运行 0.17.1 及以上版本 JuiceFS 客户端

JuiceFS S3 网关提供一个 Prometheus API 用于收集监控指标，默认地址是 `http://localhost:9567/metrics`。更多信息请查看[「JuiceFS 监控指标」](p8s_metrics.md)文档。
