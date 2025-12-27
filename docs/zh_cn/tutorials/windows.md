---
title: 在 Windows 上使用 JuiceFS
sidebar_position: 1
---

## 快速上手视频

<div className="video-container">
  <iframe
    src="//player.bilibili.com/player.html?isOutside=true&aid=114499784808051&bvid=BV1jtEczZEvq&cid=29939011077&p=1&autoplay=false"
    width="100%"
    height="360"
    scrolling="no"
    frameBorder="0"
    allowFullScreen
  ></iframe>
</div>

## 安装 JuiceFS 客户端

:::tip 环境依赖
在 Windows 系统上，JuiceFS 依赖 WinFsp 实现文件系统的挂载。你可以在 [WinFsp 源码仓库](https://github.com/winfsp/winfsp) 下载最新版本，安装后建议重启计算机，以确保所有组件正常加载。
:::

[安装文档](../getting-started/installation.md#windows) 介绍了在 Windows 上安装 JuiceFS 客户端的多种方式，这里我们展开介绍手动安装方式。

### 第一步 下载 JuiceFS 客户端

在项目仓库的 [Release 页面](https://github.com/juicedata/juicefs/releases) 下载最新版本的 JuiceFS 客户端，例如 `juicefs-1.3.0-windows-amd64.tar.gz`。

### 第二步 创建程序目录

为了便于管理，建议在系统中创建一个专用的目录来存放 JuiceFS 客户端程序。例如，可以在 `C:\` 目录下创建一个名为 `juicefs` 的文件夹，将解压后的 `juicefs.exe` 客户端程序放入该目录。

### 第三步 配置环境变量

为了在命令行中方便地使用 `juicefs` 命令，需要将 JuiceFS 客户端所在的目录添加到系统的环境变量中。具体操作如下：

1. 右键点击“此电脑”或“计算机”，选择“属性”；
2. 点击“高级系统设置”；
3. 在“系统属性”窗口中，点击“环境变量”按钮；
4. 在“系统变量”部分，找到名为 `Path` 的变量，选中后点击“编辑”；
5. 在编辑窗口中，点击“新建”，然后输入 JuiceFS 客户端所在的目录路径，例如 `C:\juicefs`；
6. 点击“确定”保存更改。

![Windows 环境变量设置](https://static1.juicefs.com/docs/windows-path.png)

### 第四步 验证安装

安装完成后，可以通过命令行验证 JuiceFS 客户端是否安装成功。打开命令提示符（CMD）或 PowerShell，输入以下命令：

```bash
juicefs version
```

如果安装成功，你应该能看到类似以下的输出：

```
juicefs version 1.3.0+2025-07-03.30190ca1094d2
```

## 创建和挂载文件系统

创建和挂载 JuiceFS 文件系统的步骤与其他操作系统类似，但需要注意 Windows 上的命令行语法和路径格式。

### 创建文件系统

```shell
juicefs format --storage oss `
    --bucket https://your-bucket.oss-cn-region.aliyuncs.com `
    --access-key your-access-key `
    --secret-key your-secret-key `
    redis://your-redis-host:6379/0 `
    mywinfs
```

> 与 Linux 系统不同，Windows 上的命令行需要使用反引号（`）来换行。

### 挂载文件系统

在 Windows 上，挂载点需要指定一个未被占用的盘符（如 X、Y、Z 等）。这与 Linux 和 macOS 上的挂载方式不同，因为这些系统是将文件系统挂载到目录中。

```shell
juicefs mount -d redis://your-redis-host:6379/0 X:
```

## 环境变量配置

从安全性的角度出发，为了避免明文输入密码，可以通过设置环境变量来存储敏感信息。这样在挂载文件系统或启用 S3 Gateway 时无需填写密码，客户端会自动从环境变量中读取。

以下是在 Windows 上使用 JuiceFS 时常用的环境变量：

| 环境变量名            | 说明                   |
|----------------------|------------------------|
| `META_PASSWORD`      | 元数据引擎密码         |
| `MINIO_ROOT_USER`    | S3 网关 Access Key     |
| `MINIO_ROOT_PASSWORD`| S3 网关 Secret Key     |

可以直接在命令行设置这些环境变量：

```cmd
set META_PASSWORD=your_password
set MINIO_ROOT_USER=your_access_key
set MINIO_ROOT_PASSWORD=your_secret_key
```

但这样的设置方式仅在当前命令行会话中有效，关闭窗口后环境变量失效，需重新设置。

### 持久化环境变量

如果希望在每次启动 Windows 时都能自动加载这些环境变量，可以通过系统环境变量设置来实现。

1. **打开系统环境变量设置**
   - 按下 `Win + S`，搜索并打开“编辑系统环境变量”。
   - 点击“环境变量”按钮。

   ![系统环境变量设置](https://static1.juicefs.com/docs/win_env_01.png)

2. **新建系统级环境变量**
   - 在“系统变量”区域点击“新建”。
   - **变量名**：例如 `META_PASSWORD`
   - **变量值**：填写密码或秘钥
   - 点击“确定”保存。

   ![添加环境变量](https://static1.juicefs.com/docs/win_env_02.png)

   ![添加环境变量](https://static1.juicefs.com/docs/win_env_03.png)

3. **验证环境变量**

    重新打开终端，尝试不带密码挂载文件系统。如果能够成功挂载，则说明环境变量已生效。

## 开机自启动挂载

通过 Windows 计划任务实现开机自动挂载有多种方式，这里介绍通过“任务计划程序”设置的方法。

1. 打开“任务计划程序”，点击“创建任务”。

   ![任务计划程序](https://static1.juicefs.com/docs/task_00.png)

2. 在“常规”选项卡中，设置任务名称（如 `JuiceFS_AutoMount`），并勾选“使用最高权限运行”。

   ![常规设置](https://static1.juicefs.com/docs/task_01.png)

3. 切换到“触发器”选项卡，点击“新建”，选择“系统启动时”作为触发条件。

   ![触发器设置](https://static1.juicefs.com/docs/task_02.png)

4. 切换到“操作”选项卡，点击“新建”，填写以下信息：

   - **程序或脚本**：浏览选择 JuiceFS 客户端路径（如 `C:\juicefs\juicefs.exe`）。
   - **参数**：填写挂载命令参数。建议将元数据引擎密码通过系统环境变量进行设置，这样可以避免在此处明文输入密码。

   ![触发器设置](https://static1.juicefs.com/docs/task_03.png)

5. 在“条件”选项卡中，勾选“仅当网络连接可用时”，以确保挂载操作在网络可用时执行。

   ![触发器设置](https://static1.juicefs.com/docs/task_04.png)

6. 点击“确定”保存任务。

**注意事项：**

- 确保挂载命令参数正确，无需在命令中包含密码（环境变量已存储）。
- 卸载文件系统：右键点击挂载盘符，选择“断开连接”。
