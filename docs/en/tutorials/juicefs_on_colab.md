---
title: Use JuiceFS on Colab with Google Cloud SQL and GCS
sidebar_position: 5
slug: /juicefs_on_colab
description: Learn how to use JuiceFS on Google Colab with Google Cloud SQL and GCS, facilitating convenient file storage and sharing in a distributed manner.
---

[Colaboratory](https://colab.research.google.com), or "Colab" for short, is a product by Google Research. Colab enables users to write and execute arbitrary Python code through the browser. It is particularly well suited for machine learning, data analysis, and educational purposes.

Colab supports Google Drive for uploading files to or downloading files from Colab instances. However, in some cases, Google Drive might not be that convenient to use with Colab. This is where JuiceFS can a valuable tool, enabling easy file synchronization between Colab instances or between a Colab instance and a local or on-premises machine.

A demo Colab notebook using JuiceFS is available [here](https://colab.research.google.com/drive/1wA8vRwqiihXkI6ViDU8Ud868UeYtmCo5).

This document outlines the necessary steps for using JuiceFS in the Colab environment. We use Google Cloud SQL as the JuiceFS metadata engine and Google Cloud Storage (GCS) as the JuiceFS object storage.

For other types of metadata engines or object storages, see [How to Set Up a Metadata Engine](../reference/how_to_set_up_metadata_engine.md)
and [How to Set Up Object Storage](../reference/how_to_set_up_object_storage.md).

Many of the steps mentioned here will be quite similar to
the [Getting Started document](../getting-started/for_distributed.md), which you can also use for reference.

## Summary of steps

1. Format a `juicefs` file system from any machine or instance with access to Google Cloud resources.
2. Mount the `juicefs` file system in a Colab Notebook
3. Store sharing files across machines and platforms.

## Prerequisites

This demo uses Google Cloud Platform's Cloud SQL and Google Cloud Storage (GCS) to create a high-performance file storage system of JuiceFS. You need a Google Cloud Platform account to follow this demo document.

If you have another cloud vendor's resources (such as AWS RDBS and S3), you can still use this guide as a reference and with other reference documents provided by JuiceFS to achieve a similar solution.

To make JuiceFS reach the best performance, you might also want the Colab instance is in the same zone or close to the region where Cloud SQL and GCS are deployed. The tutorial works for a randomly hosted Colab instance, but you might notice slower performance due to the latency between the Colab instance and the Cloud SQL/GCS regions. To start Colab instances in a specific region, see [instructions for starting a GCE VM on Colab via GCP Marketplace](https://research.google.com/colaboratory/marketplace.html).

Before diving into the detailed steps, ensure you have the following resources ready:

* A Google Cloud Platform account ready and a *project* created. This demo uses a GCP project
named `juicefs-learning`.
* A Cloud SQL (Postgres) ready for use. This demo uses the `juicefs-learning:europe-west1:juicefs-sql-example-1` instance as the metadata service.
* A GCS bucket created as the object storage service. This demo uses `gs://juicefs-bucket-example-1` as the bucket to store file chunks.
* An IAM service account or an authorized user account that has write access to the Postgres server and GCS buckets.

## Detailed steps

### Step 1: Format and mount a JuiceFS file system folder

This step needs to be done only once, and you can choose to execute it on any machine or instance where you have good connectivity and access to your Google Cloud resources.

1. Use `gcloud auth application-default login` to prepare a local credential, or use `GOOGLE_APPLICATION_CREDENTIALS` to set up a JSON key file.

2. Use [`cloud_sql_proxy`](https://cloud.google.com/sql/docs/mysql/connect-admin-proxy) to open a port (in
this case, 5432) locally to expose your cloud Postgres service to your local machine:

    ```shell
    gcloud auth application-default login

    # Or set up the json key file via GOOGLE_APPLICATION_CREDENTIALS=/path/to/key

    cloud_sql_proxy -instances=juicefs-learning:europe-west1:juicefs-sql-example-1=tcp:0.0.0.0:5432
    ```

3. Use the following command to create a new file system named `myvolume` using the `juicefs format` command. Later, you can mount this file system on any other machines or instances where you have access to your cloud resources.

    You can download `juicefs` [here](https://github.com/juicedata/juicefs/releases).

    ```shell
    juicefs format \
        --storage gs \
        --bucket gs://juicefs-bucket-example-1 \
        "postgres://postgres:mushroom1@localhost:5432/juicefs?sslmode=disable" \
        myvolume
    ```

Note that this step is only required once on any machine you prefer to work on.

### Step 2: Mount the JuiceFS file system on Colab

Once you have completed Step 1, it means you already have a JuiceFS file system (named `myvolume` in this case) defined and ready to be used.

Now, let's open a Colab page and execute the following commands to mount our file system into a folder named `mnt`.

Firstly, download the `juicefs` binary and do the same as Step 1 to get GCP credentials and open the Cloud SQL proxy.

Note that the following commands are run in the Colab environment, so there is a `!` mark at the beginning for running shell commands.

1. Download `juicefs` to the Colab runtime instance:

    ```shell
    ! curl -sSL https://d.juicefs.com/install | sh -
    ```

2. Set up Google Cloud credentials:

    ```shell
    ! gcloud auth application-default login
    ```

3. Open `cloud_sql_proxy`:

    ```shell
    ! wget https://dl.google.com/cloudsql/cloud_sql_proxy.linux.amd64 -O cloud_sql_proxy
    ! chmod +x cloud_sql_proxy
    ! GOOGLE_APPLICATION_CREDENTIALS=/content/.config/application_default_credentials.json nohup ./cloud_sql_proxy -instances=juicefs-learning:europe-west1:juicefs-sql-example-1=tcp:0.0.0.0:5432 >> cloud_sql_proxy.log &
    ```

4. Mount the `myvolumn` JuiceFS file system onto the `mnt` folder:

    ```shell
    ! GOOGLE_APPLICATION_CREDENTIALS=/content/.config/application_default_credentials.json nohup juicefs mount  "postgres://postgres:mushroom1@localhost:5432/juicefs?sslmode=disable" mnt > juicefs.log &
    ```

Now you should be able to use the `mnt` folder as if it were a local file system folder to write and read folders and files in it.

### Step 3: Load data at another time or on another instance

With data stored in the JuiceFS file system in Step 2, you can repeat all the operations mentioned in Step 2 at any time on any other machines to access the previously stored data or to store more data into it.

Congratulations! Now you have learned how to use JuiceFS, specifically with Google Colab to
conveniently share and store data files in a distributed fashion.

Feel free to explore a demo Colab notebook using JuiceFS [here](https://colab.research.google.com/drive/1wA8vRwqiihXkI6ViDU8Ud868UeYtmCo5).

Happy coding :)
