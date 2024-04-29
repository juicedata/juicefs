---
title: 在 Colab 上通过 Google CloudSQL 和 GCS 使用 JuiceFS
sidebar_position: 5
slug: /juicefs_on_colab
---

[Colaboratory](https://colab.research.google.com), 或者简称“Colab”, 是 Google Research 的产品，它允许任何人通过浏览器编写和执行 Python 代码，特别适合机器学习、数据分析和教育。
Colab 支持从 Google Drive 将文件上传到 Colab 实例或从 Colab 实例下载文件。然而在某些情况下，Google Drive 可能不太方便与 Colab 一起使用，在这种情况下，JuiceFS 是一个很有用的工具，因为他允许在 Colab 实例之间，或在 Colab 实例与本地或本地机器之间轻松的同步文件。[这里是一个使用了 JuiceFS 的 Colab 笔记本示例](https://colab.research.google.com/drive/1wA8vRwqiihXkI6ViDU8Ud868UeYtmCo5)

说明下在 Colab 环境中使用 JuiceFS 的必要步骤。我们使用 Google CloudSQL 作为 JuiceFS 的元数据引擎，使用 Google Cloud Storage (GCS) 作为 JuiceFS 的对象存储。其他类型的元数据引擎与对象存储可以参考 [如何设置元数据引擎](../reference/how_to_set_up_metadata_engine.md) 和 [如何设置对象存储](../reference/how_to_set_up_object_storage.md)。

下面将要提到的很多步骤你可以也参考 [快速上手指南](../getting-started/for_distributed.md)。

## 步骤

1. 在任何一个可以访问 Google Cloud 资源的机器或者实例上格式化一个 JuiceFS 文件系统
2. 挂载 JuiceFS 文件系统到 Colab Notebook 上
3. 愉快的跨平台跨机器分享存储的文件

## 先决条件

在这个示例中，我们使用了 Google Cloud 平台的 CloudSQL 和 Google Cloud Storage (GCS) 来创建一个高性能的 JuiceFS 文件系统。因此它需要你有一个 Google Cloud 平台的账户才能按照文档操作下去。
或者如果你有其他云平台的资源（比如 AWS 的 RDBS 和 S3），您也可以根据本指南和其他参考文档，以实现类似的解决方案。

您可能还希望 Colab 实例位于同一区域或靠近部署 CloudSQL 和 GCS 的区域使 JuiceFS 达到最佳性能。该教程适用于随机托管的 Colab 实例，所以您或许注意到了由于 Colab 实例和 CloudSQL/GCS 区域之间的延迟而导致 JuiceFS 性能缓慢。如果想要实例在特定地区去启动 Colab，可以参考[通过 GCP Marketplace 在 Colab 上启动 GCE 虚拟机](https://research.google.com/colaboratory/marketplace.html)

按照本指南操作前，您需要准备好以下资源：

* 谷歌云平台账户需要准备就绪，还要创建了一个 *project* 。就这个示例而言，我们将创建 `juicefs-learning` GCP 项目作为演示项目
* 准备使用的 CloudSQL（Postgres）。在本演示中使用实例 `juicefs-learning:europe-west1:juicefs-sql-example-1` 作为元数据服务
* 创建的 GCS 桶作为对象存储服务。在这个演示中，我们将使用`gs://juicefs-bucket-example-1`作为存储文件的桶。
* 对 Postgres 服务器和 GCS 存储桶具有写入访问权限的服务账户或授权用户帐户

## 详细步骤

### 步骤 1 - 创建并挂载一个 JuiceFS 文件系统

这个步骤只需要操作一次，你可以在任何可以访问你的 Google Cloud 资源的机器或者实例上执行。
在这里例子中，我将在我的本地机器上操作，首先你可以使用 `gcloud auth application-default login` 获取本地的凭证，或者使用 `GOOGLE_APPLICATION_CREDENTIALS` 设置 JSON 凭证文件。
然后你可以使用 [Cloud SQL 代理功能](https://cloud.google.com/sql/docs/mysql/connect-admin-proxy) 将你的 Postgres 云服务暴露在你本地机器上的一个端口上（这里是 5432）。

```shell
gcloud auth application-default login

# 或者设置 JSON 凭证文件 GOOGLE_APPLICATION_CREDENTIALS=/path/to/key

cloud_sql_proxy -instances=juicefs-learning:europe-west1:juicefs-sql-example-1=tcp:0.0.0.0:5432
```

然后使用 `juicefs format` 命令创建一个名为“myvolume”的新文件系统。之后将此文件系统挂载到您可以访问云资源的任何其他机器/实例中。
你可以在[这里](https://github.com/juicedata/juicefs/releases)下载 JuiceFS。

```shell
juicefs format \
    --storage gs \
    --bucket gs://juicefs-bucket-example-1 \
    "postgres://postgres:mushroom1@localhost:5432/juicefs?sslmode=disable" \
    myvolume
```

再次提醒：这个步骤只需要被执行一次。

### 步骤 2 - 挂载 JuiceFS 到 Colab

完成上述步骤 1 后，这意味着您已经有一个 JuiceFS 文件系统（此案例中为“myvolume”）并准备就绪可以使用了。
因此，在这里，我们打开一个 Colab 页面并运行这些命令，将我们的文件系统挂载到一个名为“mnt”的文件夹中。
首先我们下载 JuiceFS 二进制然后按照步骤一操作获取 GCP 的凭证和打开 Cloud SQL 代理。
请注意，以下命令在 Colab 环境中运行，一个 `!` 在开头意味着开始运行 shell 命令。

1. 下载 `JuiceFS`到 Colab 实例上

   ```shell
   ! curl -sSL https://d.juicefs.com/install | sh -
   ```

2. 设置 Google Cloud 凭证

   ```shell
   ! gcloud auth application-default login
   ```

3. 打开 cloud_sql 代理

   ```shell
   ! wget https://dl.google.com/cloudsql/cloud_sql_proxy.linux.amd64 -O cloud_sql_proxy
   ! chmod +x cloud_sql_proxy
   ! GOOGLE_APPLICATION_CREDENTIALS=/content/.config/application_default_credentials.json nohup ./cloud_sql_proxy -instances=juicefs-learning:europe-west1:juicefs-sql-example-1=tcp:0.0.0.0:5432 >> cloud_sql_proxy.log &
   ```

4. 挂载 JuiceFS file system `myvolumn` 到 `mnt` 目录上。

   ```shell
   ! GOOGLE_APPLICATION_CREDENTIALS=/content/.config/application_default_credentials.json nohup juicefs mount  "postgres://postgres:mushroom1@localhost:5432/juicefs?sslmode=disable" mnt > juicefs.log &
   ```

现在你应该可以像使用本地文件系统一样使用 `mnt` 目录了。

### 步骤 3 - 在任意时间从其他实例加载数据

现在，由于您在 JuiceFS 文件系统中的第 2 步中存储了数据，因此您可以随时在任何其他机器中重复第 2 步中提到的所有操作，以便再次访问之前存储的数据或存储更多数据。

恭喜！现在您已经学会了如何使用 JuiceFS，特别是如何将其与 Google Colab 一起以分布式的方式共享和存储数据文件。
[一个使用了 JuiceFS 的 Colab 笔记本示例](https://colab.research.google.com/drive/1wA8vRwqiihXkI6ViDU8Ud868UeYtmCo5)

愉快的编码吧 :）
