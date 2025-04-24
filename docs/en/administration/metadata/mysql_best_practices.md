---
sidebar_label: MySQL
sidebar_position: 2
---
# MySQL Best Practices

For distributed file systems where data and metadata are stored separately, the read and write performance and security of metadata directly affects the efficiency and data security of the whole system, respectively.

In the production environment, it is recommended to select hosted cloud databases provided by cloud computing platforms first, and comebine it with appropriate high availability architecture to use.

Please always pay attention to the integrity and security of metadata when using JuiceFS no matter whether databases is build on your own or in the cloud.

## Passing sensitive information via environment variables

Database password can be set directly through the metadata URL. Although it is easy and convenient, the password may leak during logging and process outputing processes. For the sake of security, it's better to pass the database password through an environment variable.

`META_PASSWORD` is a predefined environment variable for the database password:

```shell
export META_PASSWORD=mypassword
juicefs mount -d "mysql://user:@(192.168.1.6:3306)/juicefs" /mnt/jfs
```

## Database connection control

MySQL is a multiple threads database, every client connection need a dedicate server thread, limition of total connections and new connects are prefered. JuiceFS now provides the following options for better control of the connections:

- max_open_conns: The maximim database connections allowed for this mount point, default value is 0 which means ulimited connections. If a non-zero values is provided, lower limit may cause current requests have to wait for other reqeusts to free the database connections under high concurrency, while higher value may waste the server side resources. Dynamicly adjusting is prefered based on real business trafics.
- max_idle_conns: The minimum database connections allowed for this mount point, default values is double of logical CPU cores. Lower value will bring new database connetions under peak time, while higher value may waste some server side resource and get other mount points lack of database connections in peak time.
- max_idle_time: The maximum idle time allowed for a database connection, default value is 300 seconds. If a connection has no request to database for a given time, it will be closed to free the server side resource. Lower value will bring new database connetions under peak time.
- max_life_time: The maximum life time allowed for a database connection, default value is 0 which means unlimited. As database connections are shared with different business requests, some resources (such as memory) may not be freed cleanly or be fragmented. Provide a non-zero value (such as 3600 seconds) will let the connection to be destroyed at given time to fully release the resource associated.

We can pass the above options in metadata URL :

```shell
export META_PASSWORD=mypassword
juicefs mount -d "mysql://user:@(192.168.1.6:3306)/juicefs?max_open_conns=30&max_life_time=3600" /mnt/jfs
```

Plase refer Go official module manual [Database/SQL](https://pkg.go.dev/database/sql#SetConnMaxIdleTime) for more information.

## Periodic backups

Please refer to the official manual [Chapter 9. Backup and Recovery](https://dev.mysql.com/doc/refman/8.0/en/backup-and-recovery.html) to learn how to back up and restore databases.

It is recommended to make a plan for regularly backing up your database, and at the same time, do some tests to restore the data in an experimental environment to confirm that the backup is valid.

## High Availability

The official MySQL document [Chapter 19. Replication](https://dev.mysql.com/doc/refman/8.0/en/replication.html)  and [Chapter 20. Group Replication](https://dev.mysql.com/doc/refman/8.0/en/group-replication.html) are prefered high availability solutions. Please choose the appropriate ones according to your needs.

:::note
JuiceFS uses [transactions] to ensure atomicity of metadata operations, so a transactional storage engine such as [InnoDB](https://dev.mysql.com/doc/refman/8.0/en/backup-and-recovery.html) is required. Some MySQL based distributed (Multi-Shards) databases may not fully compatiable with MySQL both in SQL syntax or transactions, we do not have any testing or certificating works on them.
:::
