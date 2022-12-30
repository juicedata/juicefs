---
title: Use JuiceFS on Colab with Google CloudSQL and GCS
sidebar_position: 4
slug: /juicefs_on_colab
---

[Colaboratory](https://colab.research.google.com), or “Colab” for short, is a product from Google Research. Colab allows
anybody to write and execute arbitrary Python code through the browser, and is especially well suited to machine
learning, data analysis and education.

Colab supports Google Drive for upload files to or download files from Colab instances. However, in some cases, Google
Drive might not be that convenient to use with Colab. This is where JuiceFS could be a helpful tool to allow easily sync
files between Colab instances, or between Colab instance and a local or on-premises machine.

A demo Colab notebook using JuiceFS can be seen [here](https://colab.research.google.com/drive/1wA8vRwqiihXkI6ViDU8Ud868UeYtmCo5).

We illustrate the needed steps for using JuiceFS in Colab environment. We use Google CloudSQL as the JuiceFS metadata
engine, and Google Cloud Storage (GCS) as the JuiceFS object storage.

For other types of metadata engines or object storages, please refer
to [the metadata engine reference doc](../guide/how_to_set_up_metadata_engine.md)
and [the object storage reference doc](../guide/how_to_set_up_object_storage.md).

Many of the steps mentioned here will be quite similar with
the [getting started doc](../getting-started/for_distributed.md)
which you can also use for reference.

## Summary of steps

1. Format a `juicefs` file system from any machine/instance which having access to Google cloud resources
2. Mount `juicefs` file system in a Colab Notebook
3. Happily storing sharing files cross machines and platforms

## Prerequisite

In this demo we will use Google Cloud Platform's CloudSQL and Google Cloud Storage (GCS) as the backbone for creating a
massive high performance file storage system via `juicefs`. It is required that you have a Google Cloud Platform account
to follow this demo document.

Otherwise, if you have another cloud vendors' resources (for example AWS's RDBS and S3), you can still use this guide as
a reference and with other reference docs provided by `juicefs` to achieve a similar solution.

You might also want the Colab instance is in the same zone or close to the region where CloudSQL and GCS are deployed,
to make JuiceFS reaching the best performance. The tutorial should work for a random hosted Colab instance but you might
notice a slow performance due to the latency between the Colab instance and the CloudSQL/GCS regions. To start Colab
instaces in a specific region,
see [instructions for starting a GCE VM on Colab via GCP Marketplace](https://research.google.com/colaboratory/marketplace.html)
.

So in order to follow this guide you will need to have these resources ready:

* A Google Cloud Platform account ready and also a *project* created. In our case we will use GCP project
  as `juicefs-learning` for the demo
* A CloudSQL (Postgres) ready to be used. In this demo uses instance
  `juicefs-learning:europe-west1:juicefs-sql-example-1` as the metadata service
* A GCS bucket created as the object storage service. In this demo we use
  `gs://juicefs-bucket-example-1` as the bucket to store file chunks.
* An IAM ServiceAccount or an authrized user account that has write access to the posgtes server and GCS buckets

## Detailed Steps

### Step 1 - Format and mount a JuiceFS file system folder

This step is only need to be done once, and you can choose to do this at any machine/instance where you have good
connection and access to your Google Cloud resources.

In this example I am doing this on my local machine. Firstly you can use
`gcloud auth application-default login` to get a local credential ready or you can also
use `GOOGLE_APPLICATION_CREDENTIALS` to setup as JSON key file.

Then you can use [`cloud_sql_proxy`](https://cloud.google.com/sql/docs/mysql/connect-admin-proxy) to open a port (in
this case 5432) locally so to expose your cloud Postgres service to your local machine:

```shell
gcloud auth application-default login

# Or set up the json key file via GOOGLE_APPLICATION_CREDENTIALS=/path/to/key

cloud_sql_proxy -instances=juicefs-learning:europe-west1:juicefs-sql-example-1=tcp:0.0.0.0:5432
```

Then use the following command to create a new file system with a name as `myvolume` by using `juicefs format` command.
Then later we can mount this file system in any other machines/instances where you have access to your cloud resources.

You can download `juicefs` [here](https://github.com/juicedata/juicefs/releases).

```shell
juicefs format \
    --storage gs \
    --bucket gs://juicefs-bucket-example-1 \
    "postgres://postgres:mushroom1@localhost:5432/juicefs?sslmode=disable" \
    myvolume
```

Noticing again, this step above is only need to be done once at any machine you feel like to work on.

### Step 2 - Mount JuiceFS file system on Colab

After you have finished with Step 1 above, then it means you already have a JuiceFS filesystem (called `myvolume` in
this case) is defined and ready to be used.

So here we open a Colab page and run those command to mount our file system into a folder called `mnt`.

Firstly we download the `juicefs` binary and do the same as Step 1 to get GCP credentials and open cloudsql proxy.

Note that the follow commands are run in Colab environment so there is a `!` mark in the beginning for running shell command.

1. Download `juicefs` to Colab runtime instance

   ```shell
   ! curl -L -o juicefs.tar.gz https://github.com/juicedata/juicefs/releases/download/v1.0.0-beta2/juicefs-1.0.0-beta2-linux-amd64.tar.gz
   ! tar -xf juicefs.tar.gz
   ```

2. Set up Google Cloud credentials

   ```shell
   ! gcloud auth application-default login
   ```

3. Open cloud_sql_proxy

   ```shell
   ! wget https://dl.google.com/cloudsql/cloud_sql_proxy.linux.amd64 -O cloud_sql_proxy
   ! chmod +x cloud_sql_proxy
   ! GOOGLE_APPLICATION_CREDENTIALS=/content/.config/application_default_credentials.json nohup ./cloud_sql_proxy -instances=juicefs-learning:europe-west1:juicefs-sql-example-1=tcp:0.0.0.0:5432 >> cloud_sql_proxy.log &
   ```

4. Mount JuiceFS file system `myvolumn` onto folder `mnt`

   ```shell
   ! GOOGLE_APPLICATION_CREDENTIALS=/content/.config/application_default_credentials.json nohup ./juicefs mount  "postgres://postgres:mushroom1@localhost:5432/juicefs?sslmode=disable" mnt > juicefs.log &
   ```

Now you should be able to use the folder `mnt` as if it is a local file system folder to write and read folders and files
in it.

### Step 3 - Loading data at another time or another instance

Now as you have data stored in Step 2 in your JuiceFS file system, you can repeat all the operations mentioned in Step 2
anytime in any other machines so to have access again to the data previously stored or to store more data into it.

Congratulations! Now you have learned how to use JuiceFS and specifically how to use it with Google Colab to
conveniently sharing and storing data files in a distributed fashion.

A demo Colab notebook using JuiceFS can be seen [here](https://colab.research.google.com/drive/1wA8vRwqiihXkI6ViDU8Ud868UeYtmCo5).

Happy coding :)
