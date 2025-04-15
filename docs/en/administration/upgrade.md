---
sidebar_position: 9
---

# Upgrade

Upgrade methods vary with different JuiceFS clients.

## Mount point

### Normal upgrade

The JuiceFS client only has one binary file. So to upgrade the new version, you only need to replace the old one with the new one.

- **Use pre-compiled client**: Refer to [Install the pre-compiled client](../getting-started/installation.md#install-the-pre-compiled-client) for details.
- **Manually compile client**: You can pull the latest source code and recompile it to overwrite the old version of the client. Please refer to ["Installation"](../getting-started/installation.md#manually-compiling) for details.

:::caution
For the file system that has been mounted using the old version of JuiceFS client, you need to [unmount file system](../getting-started/for_distributed.md#7-unmount-the-file-system), and then re-mount it with the new version of JuiceFS client.

When unmounting the file system, make sure that no application is accessing it. Otherwise the unmount will fail. Do not forcibly unmount the file system, as it may cause the application unable to continue to access it as expected.
:::

### Smooth upgrade

Starting from version v1.2, JuiceFS supports the smooth upgrade feature, which allows you to mount JuiceFS again at the same mount point to achieve a seamless client upgrade. In addition, this feature can also be used to dynamically adjust mount parameters.

Here are two common scenarios for illustration:

- Client upgrade
    For example, if you have a `juicefs mount` process like `juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs -d` and want to upgrade to a new JuiceFS client without unmounting, perform the following steps:

    ```shell
    # 1. Backup the current binary
    cp juicefs juicefs.bak
   
    # 2. Download the new binary to overwrite the current juicefs binary
   
    # 3. Execute the juicefs mount command again to complete the smooth upgrade
    juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs -d
    ```

- Dynamically adjusting mount parameters

    For example, if you have a `juicefs mount` process like `juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs -d` and want to adjust the log level to debug without unmounting, execute the following command:

```shell
# Adjust the log level
juicefs mount redis://127.0.0.1:6379/0 /mnt/jfs --debug -d
    ```

Notes:

- Smooth upgrades require both old and new JuiceFS client versions to be v1.2 or higher.

- The FUSE parameters in the new mount parameters should be consistent with the old mount parameters, otherwise the smooth upgrade will overwrite the mount at the current mount point.

- When `enable-xattr` is enabled, smooth upgrade will overwrite the mount at the current mount point.

## Kubernetes CSI Driver

Please refer to [official documentation](https://juicefs.com/docs/csi/upgrade-csi-driver) to learn how to upgrade JuiceFS CSI Driver.

## S3 Gateway

Like [mount point](#mount-point), upgrading S3 Gateway is to replace the old version with the new version.

If it is [deployed through Kubernetes](../guide/gateway.md#deploy-in-kubernetes), you need to upgrade according to the specific deployment method, which is described in detail below.

### Upgrade via kubectl

Download and modify the `juicedata/juicefs-csi-driver` image tag in S3 Gateway [deploy YAML](https://github.com/juicedata/juicefs/blob/main/deploy/juicefs-s3-gateway.yaml) to the version you want to upgrade (see [here](https://github.com/juicedata/juicefs-csi-driver/releases) for a detailed description of all versions), and then run the following command:

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

Please refer to [Install and compile the client](../deployment/hadoop_java_sdk.md#install-and-compile-the-client) to learn how to install the new version of the Hadoop Java SDK, and then follow steps in [Deploy the client](../deployment/hadoop_java_sdk.md#deploy-the-client) to redeploy the new version of the client to complete the upgrade.

:::note
Some components must be restarted to use the new version of the Hadoop Java SDK. Please refer to the ["Restart Services"](../deployment/hadoop_java_sdk.md#restart-services) for details.
:::
