---
title: Python SDK
sidebar_position: 6
---

The JuiceFS Community Edition introduced the Python SDK in v1.3.0, making it suitable for containerized or virtualized environments where FUSE mounting is not available. The Python SDK also implements the `fsspec` interface, enabling easy integration with frameworks such as Ray.

## Compilation

You can compile the Python SDK directly in your current working environment or use a Docker container. Both methods require you to first clone the repository and navigate to the SDK directory.

```bash
# Clone JuiceFS repository
git clone https://github.com/juicedata/juicefs.git
# Enter JuiceFS directory
cd juicefs/sdk/python
```

### Direct Compilation

Direct compilation requires `go1.20+` and `python3` environments.

#### Step 1: Compile libjfs.so

```bash
go build -buildmode c-shared -ldflags="-s -w" -o juicefs/juicefs/libjfs.so ../java/libjfs
```

The compiled `libjfs.so` and `libjfs.h` files will be in the `sdk/python/juicefs/juicefs` directory.

#### Step 2: Compile Python SDK

```bash
cd juicefs && python3 -m build -w
```

The compiled Python SDK will be in the `juicefs/sdk/python/dist` directory, named `juicefs-1.3.0-py3-none-any.whl`.

### Docker Compilation

Using Docker containers for compilation requires `Docker`, `make`, and `go1.20+` installed on your system.

#### Step 1: Build Docker image

```bash
# For arm64
make arm-builder

# For amd64
make builder
```

#### Step 2: Compile Python SDK

```bash
make juicefs
```

The compiled Python SDK will be in the `juicefs/sdk/python/dist` directory, named `juicefs-1.3.0-py3-none-any.whl`.

### Compilation Error Handling

If you encounter an error like `sed: 1: "juicefs/setup.py": invalid command code j` during compilation, you can try commenting out the `sed`-related commands in the `Makefile`.

## Installation and Usage

### Installing the SDK

Copy the compiled `juicefs-1.3.0-py3-none-any.whl` file to the target machine and install it using `pip`:

```bash
pip install juicefs-1.3.0-py3-none-any.whl
```

### Preparing the File System

:::tip
JuiceFS Python SDK currently does not support formatting a file system, so please ensure you have already created a JuiceFS file system before use.
:::

Let's assume there is a pre-created file system named `myfs` with metadata engine URL `redis://192.168.1.8/0`.

### Using the Client

The `Client` class implementation is similar to Python's io module.

You can instantiate a JuiceFS client with the following code, where the `name` parameter is the file system name and the `meta` parameter is the URL of the metadata engine. The `name` parameter must exist but can be an empty string or `None`.

```python
from juicefs import Client

# Create JuiceFS client
jfs = Client(name='', meta='redis://192.168.1.8/0')

# List files in a directory
jfs.listdir('/')
```

### Using fsspec

JuiceFS Python SDK also supports the `fsspec` interface to operate the JuiceFS file system.

```bash
# Install fsspec
pip install fsspec
```

Using `fsspec` is similar to using the `Client` class, but you need to specify `jfs` or `juicefs` as the file system type.

```python
import fsspec
from juicefs.spec import JuiceFS

jfs = fsspec.filesystem('jfs', name='', meta='redis://192.168.1.8/0')

# List files in a directory
jfs.ls('/')
```

### Getting Help Information

You can use the `help()` function to get help information for classes and methods.

```python
import juicefs

help(juicefs.Client)
```

You can also use the `dir()` function to get a list of classes and methods.

```python
import juicefs

dir(juicefs.Client)
```
