# Upgrade

The upgrade methods of different JuiceFS client are different, which are described below.

## Mount point

The JuiceFS client only has one binary file, so to upgrade the new version, you only need to replace the old one with the new one.

- **Use pre-compiled client**: You can refer to the installation method of the corresponding system in [this document](../getting-started/installation.md#install-the-pre-compiled-client), download the latest client, and overwrite the old one.
- **Manually compile client**: You can pull the latest source code and recompile it to overwrite the old version of the client. Please refer to ["Installation"](../getting-started/installation.md#manually-compiling) document for more information.

:::caution
For the file system that has been mounted using the old version of JuiceFS client, you need to [unmount file system](../getting-started/for_distributed.md#7-unmounting-the-file-system), and then re-mount it with the new version of JuiceFS client.

When unmounting the file system, make sure that no application is accessing it, otherwise the unmount will fail. Do not forcibly unmount the file system, as it may cause the application to be unable to continue to access it normally.
:::

## Kubernetes CSI Driver

Please refer to [official documentation](https://juicefs.com/docs/csi/upgrade-csi-driver) to learn how to upgrade JuiceFS CSI Driver.

## S3 Gateway

Like [mount point](#mount-point), upgrading S3 Gateway is to replace the old version with the new version.

If it is [deployed through Kubernetes](../deployment/s3_gateway.md#deploy-juicefs-s3-gateway-in-kubernetes), you need to upgrade according to the specific deployment method, which is described in detail below.

### Upgrade via kubectl

Download and modify the `juicedata/juicefs-csi-driver` image tag in S3 Gateway [deploy YAML](https://github.com/juicedata/juicefs/blob/main/deploy/juicefs-s3-gateway.yaml) as the version you want to upgrade (see [here](https://github.com/juicedata/juicefs-csi-driver/releases) for a detailed description of all versions), then run the following command:

```shell
kubectl apply -f ./juicefs-s3-gateway.yaml
```

### Upgrade via Helm

Please run the following commands in sequence to upgrade the S3 Gateway:

```shell
helm repo update
helm upgrade juicefs-s3-gateway juicefs-s3-gateway/juicefs-s3-gateway -n kube-system -f ./values.yaml
```

## Hadoop Java SDK

Please refer to the ["Install and compile the client"](../deployment/hadoop_java_sdk.md#install-and-compile-the-client) document to learn how to install the new version of the Hadoop Java SDK, and then follow the ["Deploy the client"](../deployment/hadoop_java_sdk.md#deploy-the-client) steps to redeploy the new version of the client to complete the upgrade.

:::note
Some components must be restarted before using the new version of the Hadoop Java SDK. For details, please refer to the ["Restart Services"](../deployment/hadoop_java_sdk.md#restart-services) document.
:::
