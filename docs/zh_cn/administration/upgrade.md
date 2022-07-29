# 客户端升级

不同 JuiceFS 客户端的升级方式不同，以下分别介绍。

## 挂载点

JuiceFS 客户端只有一个二进制程序，升级新版只需用新版程序替换旧版程序即可。

- **使用预编译客户端**：可以参照[「安装」](../getting-started/installation.md#安装预编译客户端)文档中相应系统的安装方法，下载最新的客户端，覆盖旧版客户端即可。
- **手动编译客户端**：可以拉取最新的源代码重新编译，覆盖旧版客户端即可，具体请参考[「安装」](../getting-started/installation.md#手动编译客户端)文档。

:::caution 注意
对于已经使用旧版 JuiceFS 客户端挂载好的文件系统，需要先[卸载文件系统](../getting-started/for_distributed.md#7-卸载文件系统)，然后用新版 JuiceFS 客户端重新挂载。

卸载文件系统时需确保没有任何应用正在访问，否则将会卸载失败。不可强行卸载文件系统，有可能造成应用无法继续正常访问。
:::

## Kubernetes CSI 驱动

请参考[官方文档](https://juicefs.com/docs/zh/csi/upgrade-csi-driver)了解如何升级 JuiceFS CSI 驱动。

## S3 网关

与[挂载点](#挂载点)一样，升级 S3 网关也是使用新版程序替换旧版程序即可。

如果是[通过 Kubernetes 部署](../deployment/s3_gateway.md#在-kubernetes-中部署-s3-网关)，则需要根据具体部署的方式来升级，以下详细介绍。

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
