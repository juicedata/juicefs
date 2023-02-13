---
sidebar_label: PostgreSQL
sidebar_position: 2
slug: /postgresql_best_practices
---
# PostgreSQL Best Practices

For distributed file systems where data and metadata are stored separately, the read and write performance and security of metadata directly affects the efficiency and data security of the whole system, respectively.

In the production environment, it is recommended to select hosted cloud databases provided by cloud computing platforms first, and comebine it with appropriate high availability architecture to use.

Please always pay attention to the integrity and security of metadata when using JuiceFS no matter whether databases is build on your own or in the cloud.

## Communication Security

By default, JuiceFS clients will use SSL encryption to connect to PostgreSQL. If SSL encryption is not enabled on the database, you need to append the `sslmode=disable` parameter to the metadata URL.

It is recommended to configure and keep SSL encryption enabled on the database server side all the time.

## Passing sensitive information via environment variables

Database password can be set directly through the metadata URL. Although it is easy and convenient, the password may leak during logging and process outputing processes. For the sake of security, it's better to pass the database password through an environment variable.

`META_PASSWORD` is a predefined environment variable for the database password:

```shell
export META_PASSWORD=mypassword
juicefs mount -d "postgres://user@192.168.1.6:5432/juicefs" /mnt/jfs
```

## Authentication methods

PostgreSQL supports the md5 authentication method. The following section can be adapted in the pg_hba.conf of your PostgreSQL instance.

```
# TYPE  DATABASE        USER            ADDRESS                 METHOD
host    juicefs         juicefsuser     192.168.1.0/24          md5
```

## Periodic backups

Please refer to the official manual [Chapter 26. Backup and Restore](https://www.postgresql.org/docs/current/backup.html) to learn how to back up and restore databases.

It is recommended to make a plan for regularly backing up your database, and at the same time, do some tests to restore the data in an experimental environment to confirm that the backup is valid.

## Using connection pooler

Connection pooler is a middleware that works between client and database and reuses the earlier connection from the pool, which improve connection efficiency and reduce the loss of short connections. Commonly used connection poolers are [PgBouncer](https://www.pgbouncer.org) and [Pgpool-II](https://www.pgpool.net).

## High Availability

The official PostgreSQL document [High Availability, Load Balancing, and Replication](https://www.postgresql.org/docs/current/different-replication-solutions.html) compares several common databases in terms of high availability solutions. Please choose the appropriate ones according to your needs.

:::note
JuiceFS uses [transactions](https://www.postgresql.org/docs/current/tutorial-transactions.html) to ensure atomicity of metadata operations. Since PostgreSQL does not yet support Multi-Shard (Distributed) transactions, do not use a multi-server distributed architecture for the JuiceFS metadata.
:::
