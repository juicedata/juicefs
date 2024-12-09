---
sidebar_position: 9
---

# 客户端升级

不同 JuiceFS 客户端的升级方式不同，以下分别介绍。

## 挂载点

### 普通升级

JuiceFS 客户端只有一个二进制程序，升级新版只需用新版程序替换旧版程序即可。

- **使用预编译客户端**：可以参照[「安装」](../getting-started/installation.md#install-the-pre-compiled-client)文档中相应系统的安装方法，下载最新的客户端，覆盖旧版客户端即可。
- **手动编译客户端**：可以拉取最新的源代码重新编译，覆盖旧版客户端即可，具体请参考[「安装」](../getting-started/installation.md#manually-compiling)文档。

:::caution 注意
对于已经使用旧版 JuiceFS 客户端挂载好的文件系统，需要先[卸载文件系统](../getting-started/for_distributed.md#7-卸载文件系统)，然后用新版 JuiceFS 客户端重新挂载。

卸载文件系统时需确保没有任何应用正在访问，否则将会卸载失败。不可强行卸载文件系统，有可能造成应用无法继续正常访问。
:::

### 平滑升级

JuiceFS 在 v1.2 版本中开始支持平滑升级功能，即在相同的挂载点再次挂载 JuiceFS 即可实现业务无感的客户端平滑升级。另外该功能还可以用来动态的调整挂载参数。

下面举例说明两个常用的场景

1. 客户端升级
   比如当前存在 `juicefs mount` 进程 `juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs -d`，现希望在不卸载挂载点的情况下部署新的 JuiceFS 客户端，可以执行以下步骤：

   ```shell
    # 1. 备份当前二进制
   cp juicefs juicefs.bak
   
   # 2. 下载新的二进制覆盖当前 juicefs 二进制
   
   # 3. 再次执行 juicefs mount 命令完成平滑升级
   juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs -d
    ```

2. 动态调整挂载参数

  比如当前存在 `juicefs mount` 进程 `juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs -d`，现希望在不卸载挂载点的情况下将日志级别调整为 debug，可以执行以下命令：

```shell
# 调整日志级别
juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs --debug -d
```

一些注意事项：

1. 平滑升级要求新旧进程的 JuiceFS 客户端版本都至少为 v1.2 版本。

2. 新的挂载参数中的 FUSE 参数应该与旧的挂载参数保持一致，否则平滑升级会在当前挂载点上继续覆盖挂载。

3. `enable-xattr` 开启时，平滑升级会在当前挂载点上继续覆盖挂载。

## Kubernetes CSI 驱动

请参考[官方文档](https://juicefs.com/docs/zh/csi/upgrade-csi-driver)了解如何升级 JuiceFS CSI 驱动。

## S3 网关

与[挂载点](#挂载点)一样，升级 S3 网关也是使用新版程序替换旧版程序即可。

如果是[通过 Kubernetes 部署](../guide/gateway.md#deploy-in-kubernetes)，则需要根据具体部署的方式来升级，以下详细介绍。

### 通过 kubectl 升级

下载并修改 S3 网关[部署 YAML](https://github.com/juicedata/juicefs/blob/main/deploy/juicefs-s3-gateway.yaml) 中的 `juicedata/juicefs-csi-driver` 镜像标签为想要升级的版本（关于所有版本的详细说明请参考[这里](https://github.com/juicedata/juicefs-csi-driver/releases)），然后运行以下命令：

```shell
kubectl apply -f ./juicefs-s3-gateway.yaml
```

### 通过 Helm 升级

请依次运行以下命令以升级 S3 网关：

```shell
helm repo update
helm upgrade juicefs-s3-gateway juicefs-s3-gateway/juicefs-s3-gateway -n kube-system -f ./values.yaml
```

## Hadoop Java SDK

请参考[「安装与编译客户端」](../deployment/hadoop_java_sdk.md#安装与编译客户端)文档了解如何安装新版本的 Hadoop Java SDK，然后根据[「部署客户端」](../deployment/hadoop_java_sdk.md#部署客户端)的步骤重新部署新版本客户端即可完成升级。

:::note 注意
某些组件必须重启以后才能使用新版本的 Hadoop Java SDK，具体请参考[「重启服务」](../deployment/hadoop_java_sdk.md#重启服务)文档。
:::
