# JuiceFS client compilation and upgrade

For general users, it is recommended to directly visit the [releases](https://github.com/juicedata/juicefs/releases) page to download the pre-compiled version for installation and use.

## Compile from source code

If you want to experience the new features of JuiceFS first, you can clone the code from the `main` branch of our Github repository and manually compile the latest client.

### Clone repository

```shell
$ git clone https://github.com/juicedata/juicefs.git
```

### Compile

The JuiceFS client is developed in Go language, so before compiling, you must install the dependent tools locally in advance:

- [Go](https://golang.org) 1.15+
- GCC 5.4+

> **Tip**: For users in China, in order to download the Go modules faster, it is recommended to set the mirror server through the `GOPROXY` environment variable. For example: [Goproxy China](https://github.com/goproxy/goproxy.cn).

Enter the source code directory:

```shell
$ cd juicefs
```

Compiling:

```shell
$ make
```

After the compilation is successful, you can find the compiled `juicefs` binary program in the current directory.

## JuiceFS client upgrade

The JuiceFS client is a binary file named `juicefs`. You only need to replace the old version with the new version of the binary file when upgrading.
