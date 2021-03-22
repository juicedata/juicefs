# Use JuiceFS on Windows


## Install dependencies

JuiceFS depends on [WinFsp](http://www.secfs.net/winfsp/rel/), please install it first.


## Build JuiceFS from source

We can cross compile JuiceFS for Windows platform on Linux or macOS.

1. Install [mingw-w64](http://mingw-w64.org) on Linux or macOS. 

   On Linux, it can be installed using the distro's package manager like `yum` or `apt`.

   On macOS, use [brew](https://brew.sh/) to install: `brew install mingw-w64`

2. Build JuiceFS for Windows

```
git clone https://github.com/juicedata/juicefs.git
cd juicefs
make juicefs.exe
```


## Use JuiceFS

### Start Redis Server

JuiceFS requires a Redis, there is a [Windows version of Redis](https://github.com/tporadowski/redis),
please download the latest release and launch the redis server.


### Format JuiceFS

For test purpose, we can use a local disk to simulate a object store:

```
PS C:\> .\juicefs.exe format localhost test
2021/03/22 15:16:18.003547 juicefs[7064] <INFO>: Meta address: redis://localhost
2021/03/22 15:16:18.022972 juicefs[7064] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/03/22 15:16:18.024710 juicefs[7064] <INFO>: Data use file:///C:/jfs/local/test/
```

For other supported object storage, please check out [How To Setup Object Storage](how_to_setup_object_storage)

### Mount JuiceFS

Select an unused drive letter, such as `Z:`

```
PS C:\> .\juicefs.exe mount localhost Z:
2021/03/22 15:16:18.003547 juicefs[7064] <INFO>: Meta address: redis://localhost
2021/03/22 15:16:18.022972 juicefs[7064] <WARNING>: AOF is not enabled, you may lose data if Redis is not shutdown properly.
2021/03/22 15:16:18.024710 juicefs[7064] <INFO>: Data use file:///C:/jfs/local/test/
2021/03/22 15:16:18.024710 juicefs[7064] <INFO>: Cache: C:\Users\bob\.juicefs\cache\7088b6fa-ef2b-4792-b6c9-98fcdd6d45fb capacity: 1024 MB
The service juicefs has been started.
```


Then we can use JuiceFS as a shared disk drive `Z:`

![JuiceFS on Windows](../images/juicefs-on-windows.png)
