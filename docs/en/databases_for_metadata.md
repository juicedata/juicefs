# Metadata Engines for JuiceFS

By reading [JuiceFS Technical Architecture](architecture.md) and [How JuiceFS Store Files](how_juicefs_store_files.md), you will understand that JuiceFS is designed to store data and metadata independently. Generally , the data is stored in the cloud storage based on object storage, and the metadata corresponding to the data is stored in an independent database.

## Metadata Storage Engine

Metadata and data are equally important. The metadata records the detailed information of each file, such as the name, size, permissions, location, and so on. Especially for this kind of file system where data and metadata are stored separately, the read and write performance of metadata directly determines the actual performance of the file system.

The metadata storage of JuiceFS uses a multi-engine design. In order to create an ultra-high-performance cloud-native file system, JuiceFS first supports [Redis](https://redis.io) a key-value database running in memory, which makes JuiceFS ten times more powerful than Amazon [ EFS](https://aws.amazon.com/efs) and [S3FS](https://github.com/s3fs-fuse/s3fs-fuse) performance, [View test results](benchmark.md) .

Through active interaction with community users, we found that many application scenarios do not absolutely rely on high performance. Sometimes users just want to temporarily find a convenient tool to reliably migrate data on the cloud, or simply want to mount the object storage locally for a Small-scale use. Therefore, JuiceFS has successively opened up support for more databases such as MySQL/MariaDB and SQLite.

**But you need to pay special attention**, in the process of using the JuiceFS file system, no matter which database you choose to store metadata, please **make sure to ensure the security of the metadata**! Once the metadata is damaged or lost, it will directly cause the corresponding data to be completely damaged or lost, and in serious cases may directly cause the entire file system to be damaged.

## Redis

> [Redis](https://redis.io/) is an open source (BSD license) memory-based key-value storage system, often used as a database, cache, and message broker.

You can easily buy a cloud Redis database service on the cloud computing platform, but if you just want to quickly evaluate JuiceFS, you can use Docker to quickly run a Redis database instance on your local computer:

```shell
$ sudo docker run -d --name redis \
	-v redis-data:/data \
	-p 6379:6379 \
	--restart unless-stopped \
	redis redis-server --appendonly yes
```

> **Note**: The above command persists Redis data in Docker's `redis-data` data volume. You can modify the storage location of data persistence as needed.

> **Note**: After Redis 6.0.0, [AUTH](https://redis.io/commands/auth) command was extended with two arguments, i.e. username and password. If you use Redis < 6.0.0, just omit the username parameter in the URL, e.g. `redis://:password@host:6379/1`.

> **Security Tips**: The Redis database instance created by the above command does not enable authentication and exposes the host's `6379` port. If you want to access this database instance through the Internet, please refer to [Redis Security](https:// recommendations in redis.io/topics/security).

### Create a file system

When using Redis as the metadata storage engine, the following format is usually used to access the database:

```shell
redis://<IP or Domain name>:6379
```

If there Redis server is not running locally, the address could be specified using URL, for example, `redis://username:password@host:6379/1`, the password can also be specified by environment variable `REDIS_PASSWORD` to hide it from command line options.

For example, the following command will create a JuiceFS file system named `pics`, using the database No. `1` in Redis to store metadata:

```shell
$ juicefs format \
	--storage minio \
	--bucket http://192.168.1.6:9000/jfs \
	--access-key minioadmin \
	--secret-key minioadmin \
	redis://192.168.1.6:6379/1 \
	pics
```

### Mount a file system

```shell
$ sudo juicefs mount -d redis://192.168.1.6:6379/1 /mnt/jfs
```

> **Tip**: If you plan to share and use the same JuiceFS file system on multiple servers, you must ensure that the Redis database can be accessed by each server where the file system is to be mounted.

If you are interested, you can check [Redis Best Practices](redis_best_practices.md).

## MySQL

> MySQL is one of the most popular open source relational databases in the world, and is often used as the preferred database for Web applications.

You can easily buy a cloud MySQL database service on the cloud computing platform, but if you just want to quickly evaluate JuiceFS, you can use Docker to quickly run a MySQL database instance on your local computer:

```shell
$ sudo docker run -d --name mysql \
	-p 3306:3306 \
	-v mysql-data:/var/lib/mysql \
	-e MYSQL_ROOT_PASSWORD=password \
	-e MYSQL_DATABASE=juicefs \
	-e MYSQL_USER=user \
	-e MYSQL_PASSWORD=password \
	--restart unless-stopped \
	mysql
```

In order to make it easier for you to start the test quickly, the above code directly sets the password of the root user, the database named juicefs, and the user and user used to manage the database through the `MYSQL_ROOT_PASSWORD`, `MYSQL_DATABASE`, `MYSQL_USER`, and `MYSQL_PASSWORD` environment variables. Password, you can adjust the corresponding values of the above environment variables according to actual needs, or you can [click here to view](https://hub.docker.com/_/mysql) Docker to create more content of MySQL image.

> **Note**: The above command persists MySQL data in Docker's `mysql-data` data volume. You can modify the storage location of data persistence as needed. For the convenience of testing, port `3306` is also opened. Please do not use this instance for storage of critical data.

### Create a file system

When using MySQL as the metadata storage engine, the following format is usually used to access the database:

```shell
mysql://<username>:<password>@(<IP or Domain name>:3306)/<database-name>
```

For example:

```shell
$ juicefs format --storage minio \
	--bucket http://192.168.1.6:9000/jfs \
	--access-key minioadmin \
	--secret-key minioadmin \
	mysql://user:password@(192.168.1.6:3306)/juicefs \
	pics
```

For more examples of MySQL database address format, [click here to view](https://github.com/Go-SQL-Driver/MySQL/#examples).

### Mount a file system

```shell
$ sudo juicefs mount -d mysql://user:password@(192.168.1.6:3306)/juicefs /mnt/jfs
```

## MariaDB

> MariaDB is also one of the most popular relational databases. It is an open source branch of MySQL, maintained by the original developers of MySQL and kept open source.

Because MariaDB is highly compatible with MySQL, there is no difference in usage. For example, to create an instance on a local Docker, just change the name and mirror, and the parameters and settings are exactly the same:

```shell
$ sudo docker run -d --name mariadb \
	-p 3306:3306 \
	-v mysql-data:/var/lib/mysql \
	-e MYSQL_ROOT_PASSWORD=password \
	-e MYSQL_DATABASE=juicefs \
	-e MYSQL_USER=user \
	-e MYSQL_PASSWORD=password \
	--restart unless-stopped \
	mariadb
```

When creating and mounting a file system, keep the MySQL syntax, for example:

```shell
$ juicefs format --storage minio \
	--bucket http://192.168.1.6:9000/jfs \
	--access-key minioadmin \
	--secret-key minioadmin \
	mysql://user:password@(192.168.1.6:3306)/juicefs \
	pics
```

## SQLite

> [SQLite](https://sqlite.org) is a widely used small, fast, single-file, reliable, and full-featured SQL database engine.

The SQLite database has only one file, which is very flexible to create and use. When using it as the JuiceFS metadata storage engine, there is no need to create a database file in advance, and you can directly create a file system:

```shell
$ juicefs format --storage minio \
	--bucket https://192.168.1.6:9000/jfs \
	--access-key minioadmin \
	--secret-key minioadmin \
	sqlite3://my-jfs.db \
	pics
```

Executing the above command will automatically create a database file named `my-jfs.db` in the current directory, **please take good care of this file**！

Mount the file system:

```shell
$ sudo juicefs mount -d sqlite3://my-jfs.db
```

If the database is not in the current directory, you need to specify the absolute path of the database file, for example:

```shell
$ sudo juicefs mount -d sqlite3:///home/herald/my-jfs.db /mnt/jfs/
```

### Notice

Since SQLite is a single-file database, usually only the host where the database is located can access it. Therefore, SQLite database is more suitable for stand-alone use. For multiple servers sharing the same file system, it is recommended to use databases such as Redis or MySQL.

## TiDB

Coming soon...

## TiKV

Coming soon...

## PostgreSQL

Coming soon...