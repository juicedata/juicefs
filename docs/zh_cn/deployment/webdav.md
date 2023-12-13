---
title: 配置 WebDAV 服务
sidebar_position: 5
---

WebDAV 是 HTTP 协议的扩展，是一种便于多用户间协同编辑和管理网络上的文档的共享协议。很多涉及文件编辑和同步的工具、macOS Finder 以及一些 Linux 发行版的文件管理器都内置了 WebDAV 客户端支持。

JuiceFS 支持通过 WebDAV 协议挂载访问，对于 macOS 以及其他没有原生 FUSE 支持的操作系统，通过 WebDAV 协议访问 JuiceFS 文件系统是非常方便的。

## 前置条件

在配置 WebDAV 服务之前，你需要预先[创建一个 JuiceFS 文件系统](../getting-started/standalone.md#juicefs-format)。

## 匿名 WebDAV

对于单机或内网等安全不敏感的环境中，可以配置不带身份认证的匿名 WebDAV，命令格式如下：

```shell
juicefs webdav META-URL LISTENING-ADDRESS:PORT
```

例如，为一个 JuiceFS 文件系统启用 WebDAV 协议访问：

```shell
sudo juicefs webdav sqlite3://myjfs.db 192.168.1.8:80
```

WebDAV 服务需要通过设定的监听地址和端口进行访问，如上例中使用了内网的 IP 地址 `192.168.1.8`，以及标准的 Web 端口号 `80`，访问时无需指定端口，直接访问 `http://192.168.1.8` 即可。

如果使用了其他端口号，则需要在地址中明确指定，例如，监听 `9007` 端口，访问地址则应该用 `http://192.168.1.8:9007`。

:::tip 提示
当使用 macOS 的 Finder 访问匿名 WebDAV 时，不要使用「客人」身份。请使用「注册用户」身份，用户名可以输入任意字符，密码可以为空，然后直接连接即可。
:::

## 带身份认证的 WebDAV

:::info 说明
JuiceFS v1.0.3 及之前的版本不支持身份认证功能
:::

JuiceFS 的 WebDAV 身份认证功能需要通过环境变量设置用户名（`WEBDAV_USER`）和密码（`WEBDAV_PASSWORD`），例如：

```shell
export WEBDAV_USER=user
export WEBDAV_PASSWORD=mypassword
sudo juicefs webdav sqlite3://myjfs.db 192.168.1.8:80
```

## 启用 HTTPS 支持

JuiceFS 支持配置通过 HTTPS 协议保护的 WebDAV 服务，通过 `--cert-file` 和 `--key-file` 选项指定证书和私钥，既可以使用受信任的数字证书颁发机构 CA 签发的证书，也可以使用 OpenSSL 创建自签名证书。

### 自签名证书

这里使用 OpenSSL 创建私钥和证书：

1. 生成服务器私钥

   ```shell
   openssl genrsa -out client.key 4096
   ```

2. 生成证书签名请求（CSR）

   ```shell
   openssl req -new -key client.key -out client.csr
   ```

3. 使用 CSR 签发证书

   ```shell
   openssl x509 -req -days 365 -in client.csr -signkey client.key -out client.crt
   ```

以上三条命令会在当前目录产生以下文件：

- `client.key`：服务器私钥
- `client.csr`：证书签名请求文件
- `client.crt`：自签名证书

创建 WebDAV 服务时需要使用 `client.key` 和 `client.crt`，例如：

```shell
sudo juicefs webdav \
   --cert-file ./client.crt \
   --key-file ./client.key \
   sqlite3://myjfs.db 192.168.1.8:443
```

启用了 HTTPS 支持，监听的端口号可以改为 HTTPS 的标准端口号 `443`，然后改用 `https://` 协议头，访问时无需指定端口号，例如：`https://192.168.1.8`。

同样地，设置了非 HTTPS 标准端口号，应该在访问地址中明确指定，例如，设置了监听 `9999` 端口，访问地址应使用 `https://192.168.1.8:9999`。
