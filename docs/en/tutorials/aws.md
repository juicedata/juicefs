---
title: Use JuiceFS on AWS
sidebar_position: 4
slug: /clouds/aws
---

Amazon Web Services (AWS) is a leading global cloud computing platform that offers a wide range of cloud computing services. With its extensive product line, AWS provides flexible options for creating and utilizing JuiceFS file systems.

## Where can JuiceFS be used?

JuiceFS has a rich set of API interfaces. For AWS, JuiceFS can typically be used in the following products:

- **Amazon EC2** - Mounted using the FUSE interface
- **Amazon EKS** - Utilizing the JuiceFS CSI Driver
- **Amazon EMR** - Using the JuiceFS Hadoop Java SDK

## Preparation

A JuiceFS file system consists of two parts:

1. **Object Storage**: Used for data storage.
2. **Metadata Engine**: Used for metadata storage database.

Depending on specific requirements, you can choose to use fully managed databases and S3 object storage on AWS, or deploy them on EC2 and EKS by yourself.

> This article focuses on the method of creating a JuiceFS file system using AWS fully managed services. For self-hosted scenarios, please refer to the "[JuiceFS Supported Metadata Engines](../guide/how_to_set_up_metadata_engine.md)" and "[JuiceFS Supported Object Storage](../guide/how_to_set_up_object_storage.md)" guides, as well as the corresponding program documentation.

### Object Storage

S3 is the object storage service provided by AWS. You can create a bucket in the corresponding region as needed, or authorize the JuiceFS client to automatically create a bucket through IAM roles.

Additionally, you can use any [JuiceFS supported object storage](../guide/how_to_set_up_object_storage.md), as long as the selected object storage can be accessed by AWS services over the internet.

Amazon S3 provides the following storage types (for reference only, please refer to official AWS data for accuracy):

- **Amazon S3 STANDARD**: Standard storage, suitable for general-purpose storage with frequent data access, offering real-time access with no retrieval costs.
- **Amazon S3 STANDARD_IA**: Infrequent Access (IA) storage, suitable for data that is accessed less frequently but needs to be stored for the long term, offering real-time access with retrieval costs.
- **S3 Glacier**: Archive storage, suitable for data that is rarely accessed and requires retrieval (thawing) before access.

All object storage types that support "real-time access" can be used to build JuiceFS file systems. However, S3 Glacier requires data to be thawed before access, which means it cannot provide real-time access and thus cannot be used to build JuiceFS file systems.

In terms of storage types, it is recommended to prioritize the standard S3 type. While other storage types may have lower unit storage prices, they often come with minimum storage duration requirements and retrieval (retrieval) costs.

Furthermore, accessing object storage services requires authentication using an `access key` and `secret key`. You can refer to the document ["Controlling access to your Amazon S3 bucket"](https://docs.aws.amazon.com/AmazonS3/latest/userguide/walkthrough1.html) for creating the necessary policies. When accessing S3 from an EC2 cloud server, you can also assign an [IAM role](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles.html) to the EC2 instance to enable the S3 API to be called without using access keys.

### Database

AWS offers various web-based fully managed databases that can be used to build JuiceFS file systems, including:

- **Amazon MemoryDB for Redis**: A durable Redis in-memory database service that provides extremely fast performance.
- **Amazon RDS**: Fully managed databases such as MariaDB, MySQL, PostgresSQL, and more.

Additionally, you can use third-party fully managed databases as long as the databases can be accessed by AWS over the internet. If the environment supports it, you can also use single-node versions of SQLite or BadgerDB databases.

## Using JuiceFS on EC2

### Installing the JuiceFS Client

Please refer to the [Installation](../getting-started/installation.md) documentation to install the latest JuiceFS Community Edition client based on the operating system used by your EC2 instance.

For example, if you are using a Linux system, you can use the one-liner installation script to automatically install the client:

```shell
curl -sSL https://d.juicefs.com/install | sh -
```

### Creating a File System

#### Preparing Object Storage

You can assign an IAM role with [AmazonS3FullAccess](https://us-east-1.console.aws.amazon.com/iamv2/home?region=ap-east-1#/policies/details/arn%3Aaws%3Aiam%3A%3Aaws%3Apolicy%2FAmazonS3FullAccess) permission to your EC2 instance, allowing it to create and use S3 Buckets directly without using Access Key and Secret Key.

If you prefer to authenticate access to S3 using an Access Key and Secret Key, you can create a user in IAM and generate "Access Keys" in the security credentials section.

#### Preparing the Database

For example, if you are using MemoryDB for Redis, in order to allow EC2 instances to access the Redis cluster, you need to create them in the same VPC or add rules to the security group of the Redis cluster to allow access from the EC2 instance.

> **Note**: If you are creating a Redis 7.0 version cluster, you will need to install JuiceFS version 1.1 or above on the client side.

#### Formatting File System

```shell
juicefs format --storage s3 \
--bucket https://s3.ap-east-1.amazonaws.com/myjfs \
rediss://clustercfg.myredis.hc79sw.memorydb.ap-east-1.amazonaws.com:6379/1 \
myjfs
```

#### Mounting for Use

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

#### Automatic Mounting at Boot

Please refer to the document [Mount JuiceFS at Boot](../administration/mount_at_boot.md) for details on how to automatically mount JuiceFS at boot.

## Using JuiceFS on Amazon EKS

Amazon EKS supports two types of node:

- **Fargate** - A serverless compute engine.
- **Managed nodes** - Use Amazon EC2 as compute nodes.

JuiceFS CSI Driver is not currently supported on Fargate clusters. Please create a cluster using Managed nodes to use JuiceFS CSI Driver.

Amazon EKS is a standard Kubernetes cluster and can be managed using tools such as eksctl, kubectl, and helm. For installation and usage instructions, please refer to the [JuiceFS CSI Driver documentation](https://juicefs.com/docs/zh/csi/getting_started).

## Using JuiceFS on Amazon EMR

Please refer to the document [Using JuiceFS in Hadoop Ecosystem](../deployment/hadoop_java_sdk.md) for instructions.
