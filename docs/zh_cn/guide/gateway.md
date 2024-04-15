---
title: JuiceFS S3 网关
sidebar_position: 5
---
## 架构与原理

JuiceFS 会将文件[分块存储到底层的对象存储中](../introduction/architecture.md#how-juicefs-store-files)，暴露给用户的往往是 POSIX 接口，而如果你需要同时用 S3 兼容接口访问 JuiceFS 中的文件，就可以用到 S3 网关。其架构图如下：

![JuiceFS S3 Gateway architecture](../images/juicefs-s3-gateway-arch.png)

JuiceFS S3 网关是通过 [MinIO S3 网关](https://github.com/minio/minio/tree/ea1803417f80a743fc6c7bb261d864c38628cf8d/docs/gateway)实现的功能，我们通过实现起来其 [object 接口](https://github.com/minio/minio/blob/d46386246fb6db5f823df54d932b6f7274d46059/cmd/object-api-interface.go#L88) 并且将 JuiceFS 文件系统作为其 server 的后端存储，获得了近乎原生 MinIO; 的体验，继承 MinIO 的很多高级功能。这种架构对于 minio 来说，JuiceFS 是其运行 server 命令的一块本地盘，整体的原理类似于 `minio server /data1`。

JuiceFS Gateway 的常见的使用场景有：

* 为 JuiceFS 文件系统暴露 S3 接口，应用可以通过 S3 SDK 访问 JuiceFS 上存储的文件
* 使用 s3cmd、AWS CLI、MinIO 客户端来方便地访问和操作 JuiceFS 上存储的文件
* S3 网关还提供一个基于网页的文件管理器，使用浏览器就能对 JuiceFS 文件系统进行常规的增删管理
* 在跨集群复制数据的场景下，作为集群的统一数据出口，避免跨区访问元数据以提升数据传输性能，详见[「使用 S3 网关进行跨区域数据同步」](../guide/sync.md#sync-across-region)

## 快速开始

1. 创建文件系统

   S3 网关只是将一个 POSIX 文件系统以 S3 协议对外提供服务，所以在启动 S3 网关前需要先准备好一个的文件系统

    ```shell
    juicefs format redis://localhost:6379 test1
    ```

2. 启动 S3 网关

   使用 JuiceFS 的 `gateway` 子命令即可在当前主机启用 S3 网关。在开启功能之前，需要先设置 `MINIO_ROOT_USER` 和 `MINIO_ROOT_PASSWORD` 两个环境变量，即访问 S3 API 时认证身份用的 Access Key 和 Secret Key。这对凭证我们称为管理员凭证，拥有最高权限。例如：

    ```shell
    export MINIO_ROOT_USER=admin
    export MINIO_ROOT_PASSWORD=12345678
    ```

    ```shell
    juicefs gateway redis://localhost:6379 localhost:9000
    ```

   以上三条命令中，前两条命令用于设置环境变量。注意，`MINIO_ROOT_USER` 的长度至少 3 个字符， `MINIO_ROOT_PASSWORD` 的长度至少 8 个字符（Windows 用户请改用 `set` 命令设置环境变量，例如：`set MINIO_ROOT_USER=admin`）。

   最后一条命令用于启用 S3 网关，`gateway` 子命令至少需要提供两个参数，第一个是存储元数据的数据库 URL，第二个是 S3 网关监听的地址和端口。你可以根据需要在 `gateway` 子命令中添加[其他选项](../reference/command_reference.md#gateway)优化 S3 网关，比如，可以将默认的本地缓存设置为 20 GiB。

    ```shell
    juicefs gateway --cache-size 20480 redis://localhost:6379 localhost:9000
    ```

   在这个例子中，我们假设 JuiceFS 文件系统使用的是本地的 Redis 数据库。当 S3 网关启用时，在**当前主机**上可以使用 `http://localhost:9000` 这个地址访问到 S3 网关的管理界面。

   ![S3-gateway-file-manager](../images/s3-gateway-file-manager.jpg)

   如果你希望通过局域网或互联网上的其他主机访问 S3 网关，则需要调整监听地址，例如：

    ```shell
    juicefs gateway redis://localhost:6379 0.0.0.0:9000
    ```

   这样一来，S3 网关将会默认接受所有网络请求。不同的位置的 S3 客户端可以使用不同的地址访问 S3 网关，例如：

    - S3 网关所在主机中的第三方客户端可以使用 `http://127.0.0.1:9000` 或 `http://localhost:9000` 进行访问；
    - 与 S3 网关所在主机处于同一局域网的第三方客户端可以使用 `http://192.168.1.8:9000` 访问（假设启用 S3 网关的主机内网 IP 地址为 192.168.1.8）；
    - 通过互联网访问 S3 网关可以使用 `http://110.220.110.220:9000` 访问（假设启用 S3 网关的主机公网 IP 地址为 110.220.110.220）。

## 访问 S3 网关

各类支持 S3 API 的客户端、桌面程序、Web 程序等都可以访问 JuiceFS S3 网关。使用时请注意 S3 网关监听的地址和端口。

:::tip 提示
以下示例均为使用第三方客户端访问本地主机上运行的 S3 网关。在具体场景下，请根据实际情况调整访问 S3 网关的地址。
:::

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

为避免兼容性问题，我们推荐采用的 mc 的版本为 RELEASE.2021-04-22T17-40-00Z，你可以在这个[地址](https://dl.min.io/client/mc/release)找到历史版本和不同架构的 mc，比如这是 amd64 架构 RELEASE.2021-04-22T17-40-00Z 版本的 mc 的[下载地址](https://dl.min.io/client/mc/release/linux-amd64/archive/mc.RELEASE.2021-04-22T17-40-00Z)

下载安装完成 mc 后添加一个新的 alias：

```bash
mc alias set juicefs http://localhost:9000 admin 12345678
```

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

## 常用功能

### 多桶支持

默认情况下，juicefs gateway 只允许一个 bucket，bucket 名字为文件系统名字，如果需要多个桶，可以在启动时添加 `--multi-buckets`开启多桶支持，该参数将会把 JuiceFS 文件系统顶级目录下的每个子目录都导出为一个 bucket。创建 bucket 的行为在文件系统上的反映是顶级目录下创建了一个同名的子目录。

### 保留 etag

默认  Gateway 不会保存和返回对象的 etag 信息，可以通过`--keep-etag` 开启

### 开启对象标签

默认不支持对象标签，可以通过`--object-tag` 开启

### 启用虚拟主机风格请求

默认情况下，Gateway 支持格式为 <http://mydomain.com/bucket/object> 的路径类型请求。
`MINIO_DOMAIN` 环境变量被用来启用虚拟主机类型请求。如果请求的`Host`头信息匹配 `(.+).mydomain.com`，则匹配的模式 `$1` 被用作 bucket，并且路径被用作 object.
示例：

```shell
export MINIO_DOMAIN=mydomain.com
```

#### 调整 IAM 刷新时间

默认 IAM 缓存的刷新时间为 5 分钟，可以通过 `--refresh-iam-interval` 调整，该参数的值是一个带单位的时间字符串，例如 "300ms", "-1.5h" 或者 "2h45m"，有效的时间单位是 "ns", "us" (or "µs"), "ms", "s", "m", "h".

例如设置 1 分钟刷新

`juicefs gateway xxxx xxxx    --refresh-iam-interval 1m`

## 高级功能

JuiceFS gateway 的核心功能是对外提供 S3 接口，目前对 S3 协议的支持已经比较完善。在 v1.2 版本中，我们又添加了对身份和访问控制（IAM）和桶事件通知的支持。高级功能需要使用 RELEASE.2021-04-22T17-40-00Z 版本的 mc 命令行工具调用。这些高级功能的使用方法可以参考当时 MinIO [相关文档](https://github.com/minio/minio/tree/e0d3a8c1f4e52bb4a7d82f7f369b6796103740b3/docs)使用，也可以直接参考 mc 的命令行帮助信息。如果你不知道有哪些功能或者不知道某个功能如何使用，你可以直接在子命令后加 `-h` 查看帮助说明。下文将简要介绍支持哪些高级功能和部分示例。

### 身份和访问控制

#### 普通用户

在 v1.2 版本之前，juicefs gateway 只有在启动时创建一个超级用户，这个超级用户只属于这个进程，即使多个 gateway 的背后是同一个文件系统，其用户也都是进程间隔离的（你可以为每个 gateway 进程设置不同的超级用户，他们相互独立，互不影响）。v1.2 版本后，juicefs gateway 启动时仍需要设置超级用户，该超级用户仍旧是进程隔离的，但是允许使用 mc admin user add 添加新的用户。新添加的用户将是同文件系统共享的。新添加的用户可以使用 `mc admin user` 进行管理，支持添加，关闭，启用，删除用户，也支持查看所有用户以及展示用户信息和查看用户的策略

```Shell
$ mc admin user -h
NAME:
  mc admin user - manage users

USAGE:
  mc admin user COMMAND [COMMAND FLAGS | -h] [ARGUMENTS...]

COMMANDS:
  add      add a new user
  disable  disable user
  enable   enable user
  remove   remove user
  list     list all users
  info     display info of a user
  policy   export user policies in JSON format
  svcacct  manage service accounts
```

这里以添加用户为例子

```Shell
# 添加新用户
$ mc admin user add myminio user1 admin123

# 查看当前用户
$ mc admin user list myminio
enabled    user1

# 查看当前用户
$ mc admin user list myminio --json
{
 "status": "success",
 "accessKey": "user1",
 "userStatus": "enabled"
}
```

#### 服务账户

`mc admin user svcacct` 命令支持服务账户的管理，允许为某个用户添加服务账户，每个服务账户都与用户身份相关联，并继承附加到其父用户或父用户所属组的策略。每个访问密钥还支持可选的内联策略，可进一步限制对父用户可用的操作和资源子集的访问。

```
$ mc admin user svcacct -h
NAME:
  mc admin user svcacct - manage service accounts

USAGE:
  mc admin user svcacct COMMAND [COMMAND FLAGS | -h] [ARGUMENTS...]

COMMANDS:
  add      add a new service account
  ls       List services accounts
  rm       Remove a service account
  info     Get a service account info
  set      edit an existing service account
  enable   Enable a service account
  disable  Disable a services account
```

#### AssumeRole 安全令牌服务

Gateway 安全令牌服务（STS）是一种服务，可让客户端请求 MinIO 资源的临时凭证。临时凭证的工作原理与默认管理员凭证几乎相同，但有一些不同之处：

- 临时凭据顾名思义是短期的。它们可以配置为持续几分钟到几小时不等。证书过期后，Gateway 将不再识别它们，也不允许使用它们进行任何形式的 API 请求访问。

- 临时凭据不需要与应用程序一起存储，而是动态生成并在请求时提供给应用程序。当（甚至在）临时凭据过期时，应用程序可以请求新的凭据。

AssumeRole 会返回一组临时安全凭证，您可以使用这些凭证访问 Gateway 资源。AssumeRole 需要现有 Gateway 用户的授权凭据，返回的临时安全凭证包括访问密钥、秘密密钥和安全令牌。应用程序可以使用这些临时安全凭证对 Gateway API 操作进行签名调用。应用于这些临时凭据的策略略继承自 Gateway 用户凭据。默认情况下，AssumeRole 创建的临时安全凭证有效期为一个小时。但是，请使用可选参数 DurationSeconds 指定凭据的持续时间范围。该值从 900 秒（15 分钟）变化到最长 7 天会话持续时间限制之间不等。

##### API 请求参数

1. Version

   指示 STS API 版本信息，唯一支持的值是 '2011-06-15'。出于兼容性原因，此值借用自 AWS STS API 文档。

   | Params  | Value  |
               |---------|--------|
   | Type    | String |
   | Require | Yes    |

2. AUTHPARAMS

   指示 STS API 授权信息。如果您熟悉 AWS Signature V4 授权头部，此 STS API 支持如[此处](https://docs.aws.amazon.com/general/latest/gr/signature-version-4.html)所述的签名 V4 授权。

3. DurationSeconds

   持续时间，以秒为单位。该值可以在 900 秒（15 分钟）至 7 天之间变化。如果值高于此设置，则操作失败。默认情况下，该值设置为 3600 秒。

   | Params      | Value               |
               |-------------|---------------------|
   | *Type*      | Integer             |
   | Valid Range | 最小值为 900，最大值为 604800。 |
   | Required    | No                  |

4. Policy

   您希望将其用作内联会话策略的 JSON 格式的 IAM 策略。此参数是可选的。将策略传递给此操作会返回新的临时凭证。生成会话的权限是预设策略名称和此处设置的策略集合的交集。您不能使用该策略授予比被假定预设策略名称允许的更多权限。

   | Params      | Value             |
               |-------------|-------------------|
   | Type        | String            |
   | Valid Range | 最小长度为 1。最大长度为 2048。 |
   | Required    | No                |

##### 响应元素

此 API 的 XML 响应类似于 [AWS STS AssumeRole](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html#API_AssumeRole_ResponseElements)

##### 错误

此 API 的 XML 错误响应类似于 [AWS STS AssumeRole](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html#API_AssumeRole_Errors)

##### `POST`请求示例

```
http://minio:9000/?Action=AssumeRole&DurationSeconds=3600&Version=2011-06-15&Policy={"Version":"2012-10-17","Statement":[{"Sid":"Stmt1","Effect":"Allow","Action":"s3:*","Resource":"arn:aws:s3:::*"}]}&AUTHPARAMS
```

##### 响应示例

```
<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <AssumedRoleUser>
      <Arn/>
      <AssumeRoleId/>
    </AssumedRoleUser>
    <Credentials>
      <AccessKeyId>Y4RJU1RNFGK48LGO9I2S</AccessKeyId>
      <SecretAccessKey>sYLRKS1Z7hSjluf6gEbb9066hnx315wHTiACPAjg</SecretAccessKey>
      <Expiration>2019-08-08T20:26:12Z</Expiration>
      <SessionToken>eyJhbGciOiJIUzUxMiIsInR5cCI6IkpXVCJ9.eyJhY2Nlc3NLZXkiOiJZNFJKVTFSTkZHSzQ4TEdPOUkyUyIsImF1ZCI6IlBvRWdYUDZ1Vk80NUlzRU5SbmdEWGo1QXU1WWEiLCJhenAiOiJQb0VnWFA2dVZPNDVJc0VOUm5nRFhqNUF1NVlhIiwiZXhwIjoxNTQxODExMDcxLCJpYXQiOjE1NDE4MDc0NzEsImlzcyI6Imh0dHBzOi8vbG9jYWxob3N0Ojk0NDMvb2F1dGgyL3Rva2VuIiwianRpIjoiYTBiMjc2MjktZWUxYS00M2JmLTg3MzktZjMzNzRhNGNkYmMwIn0.ewHqKVFTaP-j_kgZrcOEKroNUjk10GEp8bqQjxBbYVovV0nHO985VnRESFbcT6XMDDKHZiWqN2vi_ETX_u3Q-w</SessionToken>
    </Credentials>
  </AssumeRoleResult>
  <ResponseMetadata>
    <RequestId>c6104cbe-af31-11e0-8154-cbc7ccf896c7</RequestId>
  </ResponseMetadata>
</AssumeRoleResponse>
```

##### AWS cli 使用 AssumeRole API

1. 启动 Gateway 并 创建 foobar 用户
2. 配置 AWS cli

   ```
   [foobar]
   region = us-east-1
   aws_access_key_id = foobar
   aws_secret_access_key = foo12345
   ```

3. 使用 AWS cli 请求 AssumeRole API

   > 注意：在以下命令中，“--role-arn”和“--role-session-name”对 Gateway 没有意义，可以设置为满足命令行要求的任何值。

   ```
   $ aws --profile foobar --endpoint-url http://localhost:9000 sts assume-role --policy '{"Version":"2012-10-17","Statement":[{"Sid":"Stmt1","Effect":"Allow","Action":"s3:*","Resource":"arn:aws:s3:::*"}]}' --role-arn arn:xxx:xxx:xxx:xxxx --role-session-name anything
   {
       "AssumedRoleUser": {
           "Arn": ""
       },
       "Credentials": {
           "SecretAccessKey": "xbnWUoNKgFxi+uv3RI9UgqP3tULQMdI+Hj+4psd4",
           "SessionToken": "eyJhbGciOiJIUzUxMiIsInR5cCI6IkpXVCJ9.eyJhY2Nlc3NLZXkiOiJLOURUSU1VVlpYRVhKTDNBVFVPWSIsImV4cCI6MzYwMDAwMDAwMDAwMCwicG9saWN5IjoidGVzdCJ9.PetK5wWUcnCJkMYv6TEs7HqlA4x_vViykQ8b2T_6hapFGJTO34sfTwqBnHF6lAiWxRoZXco11B0R7y58WAsrQw",
           "Expiration": "2019-02-20T19:56:59-08:00",
           "AccessKeyId": "K9DTIMUVZXEXJL3ATUOY"
       }
   }
   ```

##### go 应用程序访问 AssumeRole API

请参考 MinIO 官方[示例程序](https://github.com/minio/minio/blob/master/docs/sts/assume-role.go)

#### 权限管理

默认新创建的用户是没有任何权限的，需要使用 `mc admin policy` 为其赋权后才可使用。该命令支持权限的增删改查以及为用户添加删除更新权限。

```Shell
$ mc admin policy -h
NAME:
  mc admin policy - manage policies defined in the MinIO server

USAGE:
  mc admin policy COMMAND [COMMAND FLAGS | -h] [ARGUMENTS...]

COMMANDS:
  add     add new policy
  remove  remove policy
  list    list all policies
  info    show info on a policy
  set     set IAM policy on a user or group
  unset   unset an IAM policy for a user or group
  update  Attach new IAM policy to a user or group
```

gateway 内置了以下 4 种常用的策略

- readonly：只读用户
- readwrite：可读写用户
- writeonly：只写用户
- consoleAdmin：可读可写可管理，可管理指可以调用管理 API，比如创建用户等等。

例如设置某个用户为只读用户

```Shell
# 设置 user1 为只读用户
$ mc admin policy set myminio readonly user=user1

# 查看用户策略
$ mc admin user list myminio
enabled    user1                 readonly
```

以上是简单的策略，如需设置自定义的策略，可以使用 `mc admin policy add`  添加

```Shell
$ mc admin policy add -h
NAME:
  mc admin policy add - add new policy

USAGE:
  mc admin policy add TARGET POLICYNAME POLICYFILE

POLICYNAME:
  Name of the canned policy on MinIO server.

POLICYFILE:
  Name of the policy file associated with the policy name.

EXAMPLES:
  1. Add a new canned policy 'writeonly'.
     $ mc admin policy add myminio writeonly /tmp/writeonly.json
```

这里要添加的策略文件必须是一个 JSON 格式的文件，具有[IAM 兼容](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies.html)的语法，且不超过 2048 个字符。通过该语法你可以实现更为精细化的访问控制，如果你对此不熟悉，你可以先用下面的命令查看内置的这些简单的策略是如何写的并在此基础上加以更改。

```Shell
$ mc admin policy info myminio readonly
{
 "Version": "2012-10-17",
 "Statement": [
  {
   "Effect": "Allow",
   "Action": [
    "s3:GetBucketLocation",
    "s3:GetObject"
   ],
   "Resource": [
    "arn:aws:s3:::*"
   ]
  }
 ]
}
```

#### 用户组管理

JuiceFS Gateway 支持创建用户组，类似于 Linux 用户组的概念，使用 `mc admin group`  管理。你可以把一个或者多个用户设置为一个组，然后为组统一赋权。该用法与用户管理类似，就不在赘述了。

```Shell
$ mc admin  group -h
NAME:
  mc admin group - manage groups

USAGE:
  mc admin group COMMAND [COMMAND FLAGS | -h] [ARGUMENTS...]

COMMANDS:
  add      add users to a new or existing group
  remove   remove group or members from a group
  info     display group info
  list     display list of groups
  enable   enable a group
  disable  disable a group
```

#### 匿名访问管理

以上是针对有用户记录的管理，但是有时我们希望特定的对象或桶可以被任何人访问，这时就需要匿名访问管理了，这部分功能使用 `mc policy`  命令管理

```Shell
Name:
  mc policy - manage anonymous access to buckets and objects

USAGE:
  mc policy [FLAGS] set PERMISSION TARGET
  mc policy [FLAGS] set-json FILE TARGET
  mc policy [FLAGS] get TARGET
  mc policy [FLAGS] get-json TARGET
  mc policy [FLAGS] list TARGET

PERMISSION:
  Allowed policies are: [none, download, upload, public].

FILE:
  A valid S3 policy JSON filepath.

EXAMPLES:
  1. Set bucket to "download" on Amazon S3 cloud storage.
     $ mc policy set download s3/burningman2011

  2. Set bucket to "public" on Amazon S3 cloud storage.
     $ mc policy set public s3/shared

  3. Set bucket to "upload" on Amazon S3 cloud storage.
     $ mc policy set upload s3/incoming

  4. Set policy to "public" for bucket with prefix on Amazon S3 cloud storage.
     $ mc policy set public s3/public-commons/images

  5. Set a custom prefix based bucket policy on Amazon S3 cloud storage using a JSON file.
     $ mc policy set-json /path/to/policy.json s3/public-commons/images

  6. Get bucket permissions.
     $ mc policy get s3/shared

  7. Get bucket permissions in JSON format.
     $ mc policy get-json s3/shared

  8. List policies set to a specified bucket.
     $ mc policy list s3/shared

  9. List public object URLs recursively.
     $ mc policy --recursive links s3/shared/
```

#### 配置生效时间

JuiceFS Gateway 的所有管理 API 的更新操作都会立即生效并且持久化到 JuiceFS 文件系统中，而且接受该 API 请求的客户端也会立即生效。但是当 Gateway 多机运行时，情况会有所不同，因为 Gateway 在处理请求鉴权时会直接采用内存缓存信息作为校验基准，否则每次请求都读取配置文件内容作为校验基准将带来不可接受的性能问题。但是有了缓存就会存在缓存数据与配置文件不一致的问题。目前 JuiceFS Gateway 的缓存刷新策略是每 5 分钟强制更新内存缓存（部分操作也会触发缓存更新操作），这样保证多机情况下配置生效最长不会超过 5 分钟。你也可以通过 `--refresh-iam-interval` 参数来调整该时间。如果希望某个 Gateway 立即生效，可以尝试手动将其重启。

### 桶事件通知

有时用户需要根据桶上发生的事件来触发一些行为，这时就需要桶时间通知该功能了。存储桶事件通知可以用来监视存储桶中对象上发生的事件。

目前支持的对象事件类型有

- s3:ObjectCreated:Put
- s3:ObjectCreated:CompleteMultipartUpload
- s3:ObjectAccessed:Head
- s3:ObjectCreated:Post
- s3:ObjectRemoved:Delete
- s3:ObjectCreated:Copy
- s3:ObjectAccessed:Get

支持的全局事件有

- s3:BucketCreated
- s3:BucketRemoved

可以使用 mc 客户端工具通过 event 子命令设置和监听事件通知。MinIO 发送的用于发布事件的通知消息是 JSON 格式的，JSON 结构参考[这里](https://docs.aws.amazon.com/AmazonS3/latest/dev/notification-content-structure.html)。

JuiceFS Gateway 为了减少依赖，裁剪了部分支持的事件目标类型。目前存储桶事件可以支持发布到以下目标：

- Redis
- MySQL
- PostgreSQL
- WebHooks

```Shell
$ mc admin config get myminio | grep notify
notify_webhook        publish bucket notifications to webhook endpoints
notify_mysql          publish bucket notifications to MySQL databases
notify_postgres       publish bucket notifications to Postgres databases
notify_redis          publish bucket notifications to Redis datastores
```

#### 使用 Redis 发布事件

Redis 事件目标支持两种格式：`namespace` 和 `access`。

如果用的是 `namespacee` 格式，Gateway 将存储桶里的对象同步成 Redis hash 中的条目。对于每一个条目，对应一个存储桶里的对象，其 key 都被设为"存储桶名称/对象名称"，value 都是一个有关这个 Gateway 对象的 JSON 格式的事件数据。如果对象更新或者删除，hash 中对象的条目也会相应的更新或者删除。

如果使用的是 access , Gateway 使用[RPUSH](https://redis.io/commands/rpush)将事件添加到 list 中。这个 list 中每一个元素都是一个 JSON 格式的 list，这个 list 中又有两个元素，第一个元素是时间戳的字符串，第二个元素是一个含有在这个存储桶上进行操作的事件数据的 JSON 对象。在这种格式下，list 中的元素不会更新或者删除。

下面的步骤展示如何在 namespace 和 access 格式下使用通知目标。

1. 配置 Redis 到 Gateway

   使用 mc admin config set 命令配置 Redis 为 事件通知的目标

   ```Shell
   # 命令行参数
   # mc admin config set myminio notify_redis[:name] address="xxx" format="namespace|access" key="xxxx" password="xxxx" queue_dir="" queue_limit="0"
   # 具体举例
   $ mc admin config set myminio notify_redis:1 address="127.0.0.1:6379/1" format="namespace" key="bucketevents" password="yoursecret" queue_dir="" queue_limit="0"
   ```

   你可以通过 `mc admin config get myminio notify_redis` 来查看有哪些配置项，不同类型的目标其配置项也不同，针对 Redis 类型，其有以下配置项，

   ```Shell
   $ mc admin config get myminio notify_redis
   notify_redis enable=off format=namespace address= key= password= queue_dir= queue_limit=0
   ```

   每个配置项的含义

   ```Shell
   notify_redis[:name]               支持设置多个 redis，只需要其 name 不同即可
   address*     (address)            Redis 服务器的地址。例如：localhost:6379
   key*         (string)             存储/更新事件的 Redis key, key 会自动创建
   format*      (namespace*|access)  是 namespace 还是 access，默认是 'namespace'
   password     (string)             Redis 服务器的密码
   queue_dir    (path)               未发送消息的暂存目录 例如 '/home/events'
   queue_limit  (number)             未发送消息的最大限制，默认是'100000'
   comment      (sentence)           可选的注释说明
   ```

   Gateway 支持持久事件存储。持久存储将在 Redis broker 离线时备份事件，并在 broker 恢复在线时重播事件。事件存储的目录可以通过 queue_dir 字段设置，存储的最大限制可以通过 queue_limit 设置。例如，queue_dir 可以设置为/home/events, 并且 queue_limit 可以设置为 1000. 默认情况下 queue_limit 是 100000。在更新配置前，可以通过 mc admin config get 命令获取当前配置。

   ```Shell
   $ mc admin config get myminio notify_redis
   notify_redis:1 address="127.0.0.1:6379/1" format="namespace" key="bucketevents" password="yoursecret" queue_dir="" queue_limit="0"
   
   # 重启后生效
   $ mc admin config set myminio notify_redis:1 queue_limit="1000"
   Successfully applied new settings.
   Please restart your server 'mc admin service restart myminio'.
   # 注意这里无法使用 mc admin service restart myminio 重启，JuiceFS Gateway 暂不支持该功能，当使用 mc 配置后出现该提醒时需要手动重启 JuiceFS Gateway
   ```

   使用 mc admin config set 命令更新配置后，重启 JuiceFS Gateway 让配置生效。如果一切顺利，JuiceFS Gateway 会在启动时输出一行信息，类似 SQS ARNs: arn:minio:sqs::1:redis

   根据你的需要，你可以添加任意多个 Redis 目标，只要提供 Redis 实例的标识符（如上例“notify_redis:1”中的“1”）和每个实例配置参数的信息即可。

2. 启用 bucket 通知

   我们现在可以在一个叫 images 的存储桶上开启事件通知。当一个 JPEG 文件被创建或者覆盖，一个新的 key 会被创建，或者一个已经存在的 key 就会被更新到之前配置好的 Redis hash 里。如果一个已经存在的对象被删除，这个对应的 key 也会从 hash 中删除。因此，这个 Redis hash 里的行，就映射着 images 存储桶里的.jpg 对象。

   要配置这种存储桶通知，我们需要用到前面步骤 Gateway 输出的 ARN 信息。更多有关 ARN 的资料，请参考[这里](http://docs.aws.amazon.com/general/latest/gr/aws-arns-and-namespaces.html)。

   使用 mc 这个工具，这些配置信息很容易就能添加上。假设 Gateway 服务别名叫 myminio，可执行下列脚本：

   ```Shell
   mc mb myminio/images
   mc event add myminio/images arn:minio:sqs::1:redis --suffix .jpg
   mc event list myminio/images
   arn:minio:sqs::1:redis   s3:ObjectCreated:*,s3:ObjectRemoved:*,s3:ObjectAccessed:*   Filter: suffix=".jpg"
   ```

3. 验证 Redis

   启动 redis-cli 这个 Redis 客户端程序来检查 Redis 中的内容。运行 monitor Redis 命令将会输出在 Redis 上执行的每个命令的。

   ```Shell
   redis-cli -a yoursecret
   127.0.0.1:6379> monitor
   OK
   ```

   上传一个名为 myphoto.jpg 的文件到 images 存储桶。

   ```Shell
   mc cp myphoto.jpg myminio/images
   ```

   在上一个终端中，你将看到 Gateway 在 Redis 上执行的操作：

   ```Shell
   127.0.0.1:6379> monitor
   OK
   1712562516.867831 [1 192.168.65.1:59280] "hset" "bucketevents" "images/myphoto.jpg" "{\"Records\":[{\"eventVersion\":\"2.0\",\"eventSource\":\"minio:s3\",\"awsRegion\":\"\",\"eventTime\":\"2024-04-08T07:48:36.865Z\",\"eventName\":\"s3:ObjectCreated:Put\",\"userIdentity\":{\"principalId\":\"admin\"},\"requestParameters\":{\"principalId\":\"admin\",\"region\":\"\",\"sourceIPAddress\":\"127.0.0.1\"},\"responseElements\":{\"content-length\":\"0\",\"x-amz-request-id\":\"17C43E891887BA48\",\"x-minio-origin-endpoint\":\"http://127.0.0.1:9001\"},\"s3\":{\"s3SchemaVersion\":\"1.0\",\"configurationId\":\"Config\",\"bucket\":{\"name\":\"images\",\"ownerIdentity\":{\"principalId\":\"admin\"},\"arn\":\"arn:aws:s3:::images\"},\"object\":{\"key\":\"myphoto.jpg\",\"size\":4,\"eTag\":\"40b134ab8a3dee5dd9760a7805fd495c\",\"userMetadata\":{\"content-type\":\"image/jpeg\"},\"sequencer\":\"17C43E89196AE2A0\"}},\"source\":{\"host\":\"127.0.0.1\",\"port\":\"\",\"userAgent\":\"MinIO (darwin; arm64) minio-go/v7.0.11 mc/RELEASE.2021-04-22T17-40-00Z\"}}]}"
   ```

   在这我们可以看到 Gateway 在 minio_events 这个 key 上执行了 HSET 命令。

   如果用的是 access 格式，那么 minio_events 就是一个 list,Gateway 就会调用 RPUSH 添加到 list 中。再 monitor 命令中将看到

   ```Shell
   127.0.0.1:6379> monitor
   OK
   1712562751.922469 [1 192.168.65.1:61102] "rpush" "aceesseventskey" "[{\"Event\":[{\"eventVersion\":\"2.0\",\"eventSource\":\"minio:s3\",\"awsRegion\":\"\",\"eventTime\":\"2024-04-08T07:52:31.921Z\",\"eventName\":\"s3:ObjectCreated:Put\",\"userIdentity\":{\"principalId\":\"admin\"},\"requestParameters\":{\"principalId\":\"admin\",\"region\":\"\",\"sourceIPAddress\":\"127.0.0.1\"},\"responseElements\":{\"content-length\":\"0\",\"x-amz-request-id\":\"17C43EBFD35A53B8\",\"x-minio-origin-endpoint\":\"http://127.0.0.1:9001\"},\"s3\":{\"s3SchemaVersion\":\"1.0\",\"configurationId\":\"Config\",\"bucket\":{\"name\":\"images\",\"ownerIdentity\":{\"principalId\":\"admin\"},\"arn\":\"arn:aws:s3:::images\"},\"object\":{\"key\":\"myphoto.jpg\",\"size\":4,\"eTag\":\"40b134ab8a3dee5dd9760a7805fd495c\",\"userMetadata\":{\"content-type\":\"image/jpeg\"},\"sequencer\":\"17C43EBFD3DACA70\"}},\"source\":{\"host\":\"127.0.0.1\",\"port\":\"\",\"userAgent\":\"MinIO (darwin; arm64) minio-go/v7.0.11 mc/RELEASE.2021-04-22T17-40-00Z\"}}],\"EventTime\":\"2024-04-08T07:52:31.921Z\"}]"
   ```

#### 使用 MySQL 发布事件

这个通知目标支持两种格式：`namespace` 和 `access`。

如果使用的是 `namespace` 格式，Gateway 将存储桶里的对象同步成数据库表中的行。每一行有两列：key_name 和 value。key_name 是这个对象的存储桶名字加上对象名，value 都是一个有关这个 Gateway 对象的 JSON 格式的事件数据。如果对象更新或者删除，表中相应的行也会相应的更新或者删除。

如果使用的是 `access`,Gateway 将将事件添加到表里，行有两列：event_time 和 event_data。event_time 是事件在 Gateway server 里发生的时间，event_data 是有关这个 Gateway 对象的 JSON 格式的事件数据。在这种格式下，不会有行会被删除或者修改。

下面的步骤展示的是如何在`namespace`格式下使用通知目标，`access`差不多，不再赘述。

1. 确保 MySQL 版本至少满足最低要求

   JuiceFS Gateway 要求 MySQL 版本 5.7.8 及以上，因为使用了 MySQL5.7.8 版本才引入的[JSON](https://dev.mysql.com/doc/refman/5.7/en/json.html) 数据类型。

2. 配置 MySQL 到 Gateway

   使用 mc admin config set 命令配置 MySQL 为 事件通知的目标

   ```Shell
   mc admin config set myminio notify_mysql:myinstance table="minio_images" dsn_string="root:123456@tcp(172.17.0.1:3306)/miniodb"
   ```

   你可以通过 `mc admin config get myminio notify_mysql` 来查看有哪些配置项，不同类型的目标其配置项也不同，针对 MySQL 类型，其有以下配置项

   ```shell
   $ mc admin config get myminio notify_mysql
   format=namespace dsn_string= table= queue_dir= queue_limit=0 max_open_connections=2
   ```

   每个配置项的含义

   ```Shell
   KEY:
   notify_mysql[:name]  发布存储桶通知到 MySQL 数据库。当需要多个 MySQL server endpoint 时，可以为每个配置添加用户指定的“name”（例如"notify_mysql:myinstance"）.
   
   ARGS:
   dsn_string*  (string)             MySQL 数据源名称连接字符串，例如 "<user>:<password>@tcp(<host>:<port>)/<database>"
   table*       (string)             存储/更新事件的数据库表名，表会自动被创建
   format*      (namespace*|access)  'namespace'或者'access', 默认是'namespace'
   queue_dir    (path)               未发送消息的暂存目录 例如 '/home/events'
   queue_limit  (number)             未发送消息的最大限制，默认是'100000'
   comment      (sentence)           可选的注释说明
   ```

   dsn_string 是必须的，并且格式为 `<user>:<password>@tcp(<host>:<port>)/<database>`

   MinIO 支持持久事件存储。持久存储将在 MySQL 连接离线时备份事件，并在 broker 恢复在线时重播事件。事件存储的目录可以通过 queue_dir 字段设置，存储的最大限制可以通过 queue_limit 设置。例如，queue_dir 可以设置为 /home/events, 并且 queue_limit 可以设置为 1000. 默认情况下 queue_limit 是 100000.

   更新配置前，可以使用 mc admin config get 命令获取当前配置。

   ```Shell
   $ mc admin config get myminio/ notify_mysql
   notify_mysql:myinstance enable=off format=namespace host= port= username= password= database= dsn_string= table= queue_dir= queue_limit=0
   ```

   使用带有 dsn_string 参数的 mc admin config set 的命令更新 MySQL 的通知配置：

   ```Shell
   mc admin config set myminio notify_mysql:myinstance table="minio_images" dsn_string="root:xxxx@tcp(127.0.0.1:3306)/miniodb"
   ```

   请注意，根据你的需要，你可以添加任意多个 MySQL server endpoint，只要提供 MySQL 实例的标识符（如上例中的"myinstance"）和每个实例配置参数的信息即可。

   使用`mc admin config set`命令更新配置后，重启 Gateway 让配置生效。如果一切顺利，Gateway Server 会在启动时输出一行信息，类似 `SQS ARNs: arn:minio:sqs::myinstance:mysql`

3. 启用 bucket 通知

   我们现在可以在一个叫 images 的存储桶上开启事件通知，一旦上有文件上传到存储桶中，MySQL 中会 insert 一条新的记录或者一条已经存在的记录会被 update，如果一个存在对象被删除，一条对应的记录也会从 MySQL 表中删除。因此，MySQL 表中的行，对应的就是存储桶里的一个对象。

   要配置这种存储桶通知，我们需要用到前面步骤 MinIO 输出的 ARN 信息。更多有关 ARN 的资料，请参考[这里](http://docs.aws.amazon.com/general/latest/gr/aws-arns-and-namespaces.html)。

   假设 Gateway 服务别名叫 myminio，可执行下列脚本：

   ```Shell
   # 在我的 minio 中创建名为`images`的存储桶
   mc mb myminio/images
   # 使用 MySQL ARN 在“images”存储桶上添加通知配置。--suffix 参数用于过滤事件。
   mc event add myminio/images arn:minio:sqs::myinstance:mysql --suffix .jpg
   # 在“images”存储桶上打印出通知配置。
   mc event list myminio/images
   arn:minio:sqs::myinstance:mysql s3:ObjectCreated:*,s3:ObjectRemoved:*,s3:ObjectAccessed:* Filter: suffix=”.jpg”
   ```

4. 验证 MySQL

   打开一个新的 terminal 终端并上传一张 JPEG 图片到 images 存储桶。

   ```Shell
   mc cp myphoto.jpg myminio/images
   ```

   打开一个 MySQL 终端列出表 minio_images 中所有的记录。将会发现一条刚插入的记录

#### 使用 PostgreSQL 发布事件

整体方法与使用 MySQL 发布 MinIO 事件相同，这里不再累述。

需要注意的点是要求 PostgresSQL 9.5 版本及以上。Gateway 用了 PostgreSQL 9.5 引入的[INSERT ON CONFLICT](https://www.postgresql.org/docs/9.5/static/sql-insert.html#SQL-ON-CONFLICT) (aka UPSERT) 特性，以及 9.4 引入的[JSONB](https://www.postgresql.org/docs/9.4/static/datatype-json.html) 数据类型。

#### 使用 Webhook 发布事件

[Webhooks](https://en.wikipedia.org/wiki/Webhook) 采用推的方式获取数据，而不是一直去拉取。

1. 配置 webhook 到 Gateway

   Gateway 支持持久事件存储。持久存储将在 webhook 离线时备份事件，并在 broker 恢复在线时重播事件。事件存储的目录可以通过 queue_dir 字段设置，存储的最大限制可以通过 queue_limit 设置。例如， /home/events, 并且 queue_limit 可以设置为 1000. 默认情况下 queue_limit 是 100000.

   ```Shell
   KEY:
   notify_webhook[:name]  发布存储桶通知到 webhook endpoints
   
   ARGS:
   endpoint*    (url)       webhook server endpoint，例如 http://localhost:8080/minio/events
   auth_token   (string)    opaque token 或者 JWT authorization token
   queue_dir    (path)      未发送消息的暂存目录 例如 '/home/events'
   queue_limit  (number)    未发送消息的最大限制，默认是'100000'
   client_cert  (string)    Webhook 的 mTLS 身份验证的客户端证书
   client_key   (string)    Webhook 的 mTLS 身份验证的客户端证书密钥
   comment      (sentence)  可选的注释说明
   ```

   用 mc admin config set 命令更新配置。在这 endpoint 是监听 webhook 通知的服务。保存配置文件并重启 MinIO 服务让配配置生效。注意一下，在重启 MinIO 时，这个 endpoint 必须是启动并且可访问到。

   ```Shell
   mc admin config set myminio notify_webhook:1 queue_limit="0"  endpoint="http://localhost:3000" queue_dir=""
   ```

2. 启用 bucket 通知

   我们现在可以在一个叫 images 的存储桶上开启事件通知，一旦上有文件上传到存储桶中，事件将被触发。在这里，ARN 的值是 arn:minio:sqs::1:webhook。更多有关 ARN 的资料，请参考[这里](http://docs.aws.amazon.com/general/latest/gr/aws-arns-and-namespaces.html)。

   ```Shell
   mc mb myminio/images
   mc mb myminio/images-thumbnail
   mc event add myminio/images arn:minio:sqs::1:webhook --event put --suffix .jpg
   ```

3. ##### 采用 Thumbnailer 进行验证

   [Thumbnailer](https://github.com/minio/thumbnailer)  项目是一个使用 MinIO 的 listenBucketNotification API 的缩略图生成器示例，我们使用 [Thumbnailer](https://github.com/minio/thumbnailer) 来监听 Gateway 通知。如果有文件上传于是 Gateway 服务，Thumnailer 监听到该通知，生成一个缩略图并上传到 Gateway 服务。安装 Thumbnailer:

   ```Shell
   git clone https://github.com/minio/thumbnailer/
   npm install
   ```

   然后打开 Thumbnailer 的 config/webhook.json 配置文件，添加有关 MinIO server 的配置，使用下面的方式启动 Thumbnailer:

   ```Shell
   NODE_ENV=webhook node thumbnail-webhook.js
   ```

   Thumbnailer 运行在 http://localhost:3000/。下一步，配置 MinIO server，让其发送消息到这个 URL（第一步提到的），并使用 mc 来设置存储桶通知（第二步提到的）。然后上传一张图片到 Gateway server:

   ```Shell
   mc cp ~/images.jpg myminio/images
   .../images.jpg:  8.31 KB / 8.31 KB ┃▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓┃ 100.00% 59.42 KB/s 0s
   ```

   稍等片刻，然后使用 mc ls 检查存储桶的内容，你将看到有个缩略图出现了。

   ```Shell
   mc ls myminio/images-thumbnail
   [2017-02-08 11:39:40 IST]   992B images-thumbnail.jpg
   ```
