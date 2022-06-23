---
sidebar_label: 如何在 JuiceFS 上使用临时访问凭证授权的对象存储
sidebar_position: 7
slug: /juicefs_with_sts_token
---
# 如何在 JuiceFS 上使用临时访问凭证授权的对象存储

永久访问凭证一般有两个部分，accessKey，secretKey，而临时访问凭证一般包括 3 个部分，accessKey，secretKey 与 token，并且临时访问凭证具有过期时间，一般在几分钟到几个小时之间。

## 如何获取临时凭证

不同云厂商的获取方式不同，一般是需要以具有相应权限用户的 accessKey，secretKey 以及代表临时访问凭证的权限边界的 ARN 作为参数请求访问云服务厂商的 STS 服务器来获取临时访问凭证。这个过程一般可以由云厂商提供的 SDK 简化操作。比如 AWS S3 获取临时凭证方式可以参考这个[链接](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_temp_request.html)，阿里云 OSS 获取临时凭证方式可以参考这个[链接](https://help.aliyun.com/document_detail/100624.html)。

## juicefs 如何使用临时访问凭证设置对象存储

使用临时凭证的方式与使用永久凭证差异不大，在文件系统 `format` 时，将临时凭证的 accessKey， secretKey， token 分别通过 --access-key，--secret-key，--session-token 设置值即可。 例如：
```bash
$ juicefs format --storage oss --access-key xxxx --secret-key xxxx --session-token xxxx --bucket https://bucketName.oss-cn-hangzhou.aliyuncs.com redis://localhost:6379/1 test1
```

由于临时凭证很快就会过期，所以关键在于在 `format` 文件系统后，如何在临时凭证过期前更新 juicefs 正在使用的临时凭证。一次凭证更新过程分为两步:

1. 在临时凭证过期前，申请好新的临时凭证
2. 无需停止正在运行的 juicefs ，直接使用 `juicefs config Meta-URL --access-key xxxx  --secret-key xxxx --session-token xxxx` 命令热更新访问凭证

新挂载的客户端会直接使用新的凭证，已经在运行的所有客户端也会在一分钟内更新自己的凭证。整个更新过程不会影响正在运行的业务。由于临时凭证过期时间较短，所以以上步骤需要**长期循环执行**才能保证 juicefs 服务可以正常访问到对象存储。

