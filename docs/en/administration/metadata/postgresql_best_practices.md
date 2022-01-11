---
sidebar_label: PostgreSQL
sidebar_position: 2
---
# PostgreSQL Best Practices

For distributed file systems where data and metadata are stored separately, the read and write performance of metadata directly affects the efficiency of the whole system, and the security of metadata is also directly related to the data security of the whole system.

In the production environment, it is recommended that you give priority to the hosted cloud database provided by the cloud computing platform with appropriate high availability architecture.

Whether you build it yourself or use a cloud database, you should always pay attention to the integrity and security of metadata when using JuiceFS.

## Communication Security

By default, JuiceFS clients will use SSL encryption to connect to PostgreSQL. If SSL encryption is not enabled on the database, you need to append the `sslmode=disable` parameter to the metadata URL.

It is recommended to configure and always enable SSL encryption on the database server side.

## Passing sensitive information via environment variables

Although it is easy and convenient to set the database password directly in the metadata URL, the password may be leaked in logs or program output, and for data security, the database password should always be passed through an environment variable.

Environment variable names can be freely defined, e.g.

```shell
export $PG_PASSWD=mypassword
```

Passing the database password in the metadata URL via environment variables.

```shell
juicefs mount -d "postgres://user:$PG_PASSWD@192.168.1.6:5432/juicefs" /mnt/jfs
```

## Backup periodically

Please refer to the official manual [Chapter 26. Backup and Restore](https://www.postgresql.org/docs/current/backup.html) to learn how to backup and restore the database.

It is recommended to make a database backup plan and follow it periodically, and at the same time, try to restore the data in an experimental environment to confirm that the backup is valid.

## Using connection pooling

Connection pooling is an intermediate layer between the client and the database, which acts as an intermediary to improve connection efficiency and reduce the loss of short connections. Commonly used connection pools are [PgBouncer](https://www.pgbouncer.org/) and [Pgpool-II](https://www.pgpool.net/).

## High Availability

The official PostgreSQL document [High Availability, Load Balancing, and Replication](https://www.postgresql.org/docs/current/different-replication-solutions.html) compares several common database high availability solutions, please choose the appropriate according to your needs.

:::note
JuiceFS uses [transactions](https://www.postgresql.org/docs/current/tutorial-transactions.html) to ensure atomicity of metadata operations. Since PostgreSQL does not yet support Muti-Shard (Distributed) transactions, do not use a multi-server distributed architecture for the JuiceFS metadata.
:::
