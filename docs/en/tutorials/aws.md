---
title: Use JuiceFS on AWS
sidebar_position: 4
slug: /clouds/aws
---

Amazon Web Services (AWS) is a leading global cloud computing platform that offers a wide range of cloud computing services. With its extensive product line, AWS provides flexible options for creating and utilizing JuiceFS file systems.

## Where can JuiceFS be used? {#where-can-juicefs-be-used}

JuiceFS has a rich set of API interfaces. For AWS, JuiceFS can typically be used in the following products:

- **Amazon EC2**: Use by mounting the JuiceFS file system
- **Amazon Elastic Kubernetes Service (EKS)**: Utilizing the JuiceFS CSI Driver
- **Amazon EMR**: Using the JuiceFS Hadoop Java SDK

## Preparation {#preparation}

A JuiceFS file system consists of two parts:

1. **Object Storage**: Used for data storage.
2. **Metadata Engine**: A database used for storing metadata.

Depending on specific requirements, you can choose to use fully managed databases and S3 object storage on AWS, or deploy them on EC2 and EKS by yourself.

:::tip
This article focuses on the method of creating a JuiceFS file system using AWS fully managed services. For self-hosted scenarios, please refer to the ["JuiceFS Supported Metadata Engines"](../reference/how_to_set_up_metadata_engine.md) and ["JuiceFS Supported Object Storage"](../reference/how_to_set_up_object_storage.md) guides, as well as the corresponding program documentation.
:::

### Object storage {#object-storage}

S3 is the object storage service provided by AWS. You can create a bucket in the corresponding region as needed, or authorize the JuiceFS client to automatically create a bucket through [IAM roles](../reference/how_to_set_up_object_storage.md#aksk).

Amazon S3 provides various [storage classes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/storage-class-intro.html), for example:

- **S3 Standard**: Standard storage, suitable for general-purpose storage with frequent data access, offering real-time access with no retrieval costs.
- **S3 Standard-IA**: Infrequent Access (IA) storage, suitable for data that is accessed less frequently but needs to be stored for the long term, offering real-time access with retrieval costs.
- **S3 Glacier**: Archive storage, suitable for data that is rarely accessed and requires retrieval (thawing) before access.

You can set the storage class when creating or mounting the JuiceFS file system, please refer to [documentation](../reference/how_to_set_up_object_storage.md#storage-class) for details. It is recommended to choose the standard storage class first. Although other storage classes may have lower unit storage prices, they often come with minimum storage duration requirements and retrieval costs.

Furthermore, accessing object storage services requires authentication using Access Key (a.k.a. access key ID) and Secret Key (a.k.a. secret access key). You can refer to the document ["Managing access keys for IAM users"](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_access-keys.html) for creating the necessary policies. When accessing S3 from an EC2 cloud server, you can also assign an [IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html) to the EC2 instance to enable the S3 API to be called without using access keys.

### Database {#database}

AWS offers various network-based fully managed databases that can be used to build the JuiceFS metadata engine, mainly including:

- **Amazon MemoryDB for Redis** (hereinafter referred to as MemoryDB): A durable Redis in-memory database service that provides extremely fast performance.
- **Amazon RDS**: Fully managed databases such as MariaDB, MySQL, PostgreSQL, and more.

:::note
Although Amazon ElastiCache for Redis (hereinafter referred to as ElastiCache) also provides services compatible with the Redis protocol, compared with MemoryDB, ElastiCache cannot provide "strong consistency guarantee", so MemoryDB is recommended.
:::

## Using JuiceFS on EC2 {#using-juicefs-on-ec2}

### Installing the JuiceFS client {#installing-the-juicefs-client}

Please refer to the [Installation](../getting-started/installation.md) documentation to install the latest JuiceFS Community Edition client based on the operating system used by your EC2 instance.

For example, if you are using a Linux system, you can use the one-liner installation script to automatically install the client:

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

### Creating a File System {#creating-a-file-system}

#### Preparing object storage {#preparing-object-storage}

You can assign an IAM role with [AmazonS3FullAccess](https://docs.aws.amazon.com/AmazonS3/latest/userguide/security-iam-awsmanpol.html#security-iam-awsmanpol-amazons3fullaccess) permission to your EC2 instance, allowing it to create and use S3 Buckets directly without using Access Key and Secret Key.

#### Preparing the database {#preparing-the-database}

Here we take MemoryDB as an example, please refer to ["Redis Best Practices"](../administration/metadata/redis_best_practices.md) and AWS documentation to create a database.

In order to allow EC2 instances to access the Redis cluster, you need to create them in the same VPC or add rules to the security group of the Redis cluster to allow access from the EC2 instance.

:::note
If you are creating a Redis 7.0 version cluster, you will need to install JuiceFS version 1.1 or above on the client side.
:::

#### Formatting file system {#formatting-file-system}

```shell
juicefs format --storage s3 \
  --bucket https://s3.ap-east-1.amazonaws.com/myjfs \
  rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1 \
  myjfs
```

### Mounting file system {#mounting-file-system}

```shell
sudo juicefs mount -d \
  rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1 \
  /mnt/myjfs
```

To mount and use the file system created by authorizing S3 access through an IAM role from outside of AWS, you will need to use `juicefs config` to add the Access Key and Secret Key for the file system.

```shell
juicefs config \
  --access-key=<your-access-key> \
  --secret-key=<your-secret-key> \
  rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1
```

### Mounting at boot {#mounting-at-boot}

Please refer to the document [Mount JuiceFS at Boot](../administration/mount_at_boot.md) for details on how to automatically mount JuiceFS at boot.

## Using JuiceFS on Amazon EKS {#using-juicefs-on-amazon-eks}

Amazon EKS supports [three types of node](https://docs.aws.amazon.com/eks/latest/userguide/eks-compute.html):

- **EKS managed node groups**: Use Amazon EC2 as compute nodes
- **Self-managed nodes**: Use Amazon EC2 as compute nodes
- **Fargate**: A serverless compute engine

JuiceFS CSI Driver is not currently supported on Fargate. Please create a cluster using "EKS managed node groups" or "self-managed nodes" to use JuiceFS CSI Driver.

Amazon EKS is a standard Kubernetes cluster and can be managed using tools such as `eksctl`, `kubectl`, and `helm`. For installation and usage instructions, please refer to the [JuiceFS CSI Driver documentation](/docs/csi/introduction).

## Using JuiceFS on Amazon EMR {#using-juicefs-on-amazon-emr}

Please refer to the document ["Using JuiceFS in Hadoop Ecosystem"](../deployment/hadoop_java_sdk.md) for instructions.
