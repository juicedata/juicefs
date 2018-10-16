# GO SDK 文档

# 概述

本文档主要介绍BOS GO SDK的安装和使用。在使用本文档前，您需要先了解BOS的一些基本知识，并已开通了BOS服务。若您还不了解BOS，可以参考[产品描述](https://cloud.baidu.com/doc/BOS/ProductDescription.html)和[入门指南](https://cloud.baidu.com/doc/BOS/GettingStarted-new.html)。

# 安装SDK工具包

## 运行环境

GO SDK可以在go1.3及以上环境下运行。

## 安装SDK

**直接从github下载**

使用`go get`工具从github进行下载：

```shell
go get github.com/baidubce/bce-sdk-go
```

**SDK目录结构**

```text
bce-sdk-go
|--auth                   //BCE签名和权限认证
|--bce                    //BCE公用基础组件
|--http                   //BCE的http通信模块
|--services               //BCE相关服务目录
|  |--bos                 //BOS服务目录
|  |  |--bos_client.go    //BOS客户端入口
|  |  |--api              //BOS相关API目录
|  |     |--bucket.go     //BOS的Bucket相关API实现
|  |     |--object.go     //BOS的Object相关API实现
|  |     |--multipart.go  //BOS的Multipart相关API实现
|  |     |--module.go     //BOS相关API的数据模型
|  |     |--util.go       //BOS相关API实现使用的工具
|  |--sts                 //STS服务目录
|--util                   //BCE公用的工具实现
```

## 卸载SDK

预期卸载SDK时，删除下载的源码即可。


# 初始化

## 确认Endpoint

在确认您使用SDK时配置的Endpoint时，可先阅读开发人员指南中关于[BOS访问域名](https://cloud.baidu.com/doc/BOS/DevRef.html#BOS.E8.AE.BF.E9.97.AE.E5.9F.9F.E5.90.8D)的部分，理解Endpoint相关的概念。百度云目前开放了多区域支持，请参考[区域选择说明](https://cloud.baidu.com/doc/Reference/Regions.html)。

目前支持“华北-北京”、“华南-广州”和“华东-苏州”三个区域。北京区域：`http://bj.bcebos.com`，广州区域：`http://gz.bcebos.com`，苏州区域：`http://su.bcebos.com`。对应信息为：

访问区域 | 对应Endpoint
---|---
BJ | bj.bcebos.com
GZ | gz.bcebos.com
SU | su.bcebos.com

## 获取密钥

要使用百度云BOS，您需要拥有一个有效的AK(Access Key ID)和SK(Secret Access Key)用来进行签名认证。AK/SK是由系统分配给用户的，均为字符串，用于标识用户，为访问BOS做签名验证。

可以通过如下步骤获得并了解您的AK/SK信息：

[注册百度云账号](https://login.bce.baidu.com/reg.html?tpl=bceplat&from=portal)

[创建AK/SK](https://console.bce.baidu.com/iam/?_=1513940574695#/iam/accesslist)

## 新建BOS Client

BOS Client是BOS服务的客户端，为开发者与BOS服务进行交互提供了一系列的方法。

### 使用AK/SK新建BOS Client

通过AK/SK方式访问BOS，用户可以参考如下代码新建一个BOS Client：

```go
import (
	"github.com/baidubce/bce-sdk-go/services/bos"
)

func main() {
	// 用户的Access Key ID和Secret Access Key
	ACCESS_KEY_ID, SECRET_ACCESS_KEY := <your-access-key-id>, <your-secret-access-key>

	// 用户指定的Endpoint
	ENDPOINT := <domain-name>

	// 初始化一个BosClient
	bosClient, err := bos.NewClient(AK, SK, ENDPOINT)
}
```

在上面代码中，`ACCESS_KEY_ID`对应控制台中的“Access Key ID”，`SECRET_ACCESS_KEY`对应控制台中的“Access Key Secret”，获取方式请参考《操作指南 [管理ACCESSKEY](https://cloud.baidu.com/doc/BOS/GettingStarted.html#.E7.AE.A1.E7.90.86ACCESSKEY)》。第三个参数`ENDPOINT`支持用户自己指定域名，如果设置为空字符串，会使用默认域名作为BOS的服务地址。

> **注意：**`ENDPOINT`参数需要用指定区域的域名来进行定义，如服务所在区域为北京，则为`http://bj.bcebos.com`。

### 使用STS创建BOS Client

**申请STS token**

BOS可以通过STS机制实现第三方的临时授权访问。STS（Security Token Service）是百度云提供的临时授权服务。通过STS，您可以为第三方用户颁发一个自定义时效和权限的访问凭证。第三方用户可以使用该访问凭证直接调用百度云的API或SDK访问百度云资源。

通过STS方式访问BOS，用户需要先通过STS的client申请一个认证字符串，申请方式可参见[百度云STS使用介绍](https://cloud.baidu.com/doc/BOS/API.html#STS.E7.AE.80.E4.BB.8B)。

**用STS token新建BOS Client**

申请好STS后，可将STS Token配置到BOS Client中，从而实现通过STS Token创建BOS Client。

**代码示例**

GO SDK实现了STS服务的接口，用户可以参考如下完整代码，实现申请STS Token和创建BOS Client对象：

```go
import (
	"fmt"

	"github.com/baidubce/bce-sdk-go/auth"         //导入认证模块
	"github.com/baidubce/bce-sdk-go/services/bos" //导入BOS服务模块
	"github.com/baidubce/bce-sdk-go/services/sts" //导入STS服务模块
)

func main() {
	// 创建STS服务的Client对象，Endpoint使用默认值
	AK, SK := <your-access-key-id>, <your-secret-access-key>
	stsClient, err := sts.NewClient(AK, SK)
	if err != nil {
		fmt.Println("create sts client object :", err)
		return
	}

	// 获取临时认证token，有效期为60秒，ACL为空
	sts, err := stsClient.GetSessionToken(60, "")
	if err != nil {
		fmt.Println("get session token failed:", err)
		return
    }
	fmt.Println("GetSessionToken result:")
	fmt.Println("  accessKeyId:", sts.AccessKeyId)
	fmt.Println("  secretAccessKey:", sts.SecretAccessKey)
	fmt.Println("  sessionToken:", sts.SessionToken)
	fmt.Println("  createTime:", sts.CreateTime)
	fmt.Println("  expiration:", sts.Expiration)
	fmt.Println("  userId:", sts.UserId)

	// 使用申请的临时STS创建BOS服务的Client对象，Endpoint使用默认值
	bosClient, err := bos.NewClient(sts.AccessKeyId, sts.SecretAccessKey, "")
	if err != nil {
	    fmt.Println("create bos client failed:", err)
	    return
	}
	stsCredential, err := auth.NewSessionBceCredentials(
	        sts.AccessKeyId,
	        sts.SecretAccessKey,
	        sts.SessionToken)
	if err != nil {
	    fmt.Println("create sts credential object failed:", err)
	    return
	}
	bosClient.Config.Credentials = stsCredential
}
```

> 注意：
> 目前使用STS配置BOS Client时，无论对应BOS服务的Endpoint在哪里，STS的Endpoint都需配置为http://sts.bj.baidubce.com。上述代码中创建STS对象时使用此默认值。

## 配置HTTPS协议访问BOS

BOS支持HTTPS传输协议，您可以通过在创建BOS Client对象时指定的Endpoint中指明HTTPS的方式，在BOS GO SDK中使用HTTPS访问BOS服务：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos"

ENDPOINT := "https://bj.bcebos.com" //指明使用HTTPS协议
AK, SK := <your-access-key-id>, <your-secret-access-key>
bosClient, _ := bos.NewClient(AK, SK, ENDPOINT)
```

## 配置BOS Client

如果用户需要配置BOS Client的一些细节的参数，可以在创建BOS Client对象之后，使用该对象的导出字段`Config`进行自定义配置，可以为客户端配置代理，最大连接数等参数。

### 使用代理

下面一段代码可以让客户端使用代理访问BOS服务：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos"

//创建BOS Client对象
AK, SK := <your-access-key-id>, <your-secret-access-key>
ENDPOINT := "bj.bcebos.com"
client, _ := bos.NewClient(AK, SK, ENDPOINT)

//代理使用本地的8080端口
client.Config.ProxyUrl = "127.0.0.1:8080"
```

### 设置网络参数

用户可以通过如下的示例代码进行网络参数的设置：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos"

AK, SK := <your-access-key-id>, <your-secret-access-key>
ENDPOINT := "bj.bcebos.com"
client, _ := bos.NewClient(AK, SK, ENDPOINT)

// 配置不进行重试，默认为Back Off重试
client.Config.Retry = bce.NewNoRetryPolicy()

// 配置连接超时时间为30秒
client.Config.ConnectionTimeoutInMillis = 30 * 1000
```

### 配置生成签名字符串选项

```go
// import "github.com/baidubce/bce-sdk-go/services/bos"

AK, SK := <your-access-key-id>, <your-secret-access-key>
ENDPOINT := "bj.bcebos.com"
client, _ := bos.NewClient(AK, SK, ENDPOINT)

// 配置签名使用的HTTP请求头为`Host`
headersToSign := map[string]struct{}{"Host": struct{}{}}
client.Config.SignOption.HeadersToSign = HeadersToSign

// 配置签名的有效期为30秒
client.Config.SignOption.ExpireSeconds = 30
```

**参数说明**

用户使用GO SDK访问BOS时，创建的BOS Client对象的`Config`字段支持的所有参数如下表所示：

配置项名称 |  类型   | 含义
-----------|---------|--------
Endpoint   |  string | 请求服务的域名
ProxyUrl   |  string | 客户端请求的代理地址
Region     |  string | 请求资源的区域
UserAgent  |  string | 用户名称，HTTP请求的User-Agent头
Credentials| \*auth.BceCredentials | 请求的鉴权对象，分为普通AK/SK与STS两种
SignOption | \*auth.SignOptions    | 认证字符串签名选项
Retry      | RetryPolicy | 连接重试策略
ConnectionTimeoutInMillis| int     | 连接超时时间，单位毫秒，默认20分钟

说明：

  1. `Credentials`字段使用`auth.NewBceCredentials`与`auth.NewSessionBceCredentials`函数创建，默认使用前者，后者为使用STS鉴权时使用，详见“使用STS创建BOS Client”小节。
  2. `SignOption`字段为生成签名字符串时的选项，详见下表说明：

名称          | 类型  | 含义
--------------|-------|-----------
HeadersToSign |map[string]struct{} | 生成签名字符串时使用的HTTP头
Timestamp     | int64 | 生成的签名字符串中使用的时间戳，默认使用请求发送时的值
ExpireSeconds | int   | 签名字符串的有效期

     其中，HeadersToSign默认为`Host`，`Content-Type`，`Content-Length`，`Content-MD5`；TimeStamp一般为零值，表示使用调用生成认证字符串时的时间戳，用户一般不应该明确指定该字段的值；ExpireSeconds默认为1800秒即30分钟。
  3. `Retry`字段指定重试策略，目前支持两种：`NoRetryPolicy`和`BackOffRetryPolicy`。默认使用后者，该重试策略是指定最大重试次数、最长重试时间和重试基数，按照重试基数乘以2的指数级增长的方式进行重试，直到达到最大重试测试或者最长重试时间为止。


# Bucket管理

Bucket既是BOS上的命名空间，也是计费、权限控制、日志记录等高级功能的管理实体。

- Bucket名称在所有区域中具有全局唯一性，且不能修改。

> **说明：**
>
> 百度云目前开放了多区域支持，请参考[区域选择说明](https://cloud.baidu.com/doc/Reference/Regions.html)。
> 目前支持“华北-北京”、“华南-广州”和“华东-苏州”三个区域。北京区域：`http://bj.bcebos.com`，广州区域：`http://gz.bcebos.com`，苏州区域：`http://su.bcebos.com`。

- 存储在BOS上的每个Object都必须包含在一个Bucket中。
- 一个用户最多可创建100个Bucket，但每个Bucket中存放的Object的数量和大小总和没有限制，用户不需要考虑数据的可扩展性。

## Bucket权限管理

### 设置Bucket的访问权限

如下代码将Bucket的权限设置为了private。

```go
err := bosClient.PutBucketAclFromCanned(bucketName, "private")
```

用户可设置的CannedACL包含三个值：`private` 、`public-read` 、`public-read-write`，它们分别对应相关权限。具体内容可以参考BOS API文档 [使用CannedAcl方式的权限控制](https://cloud.baidu.com/doc/BOS/API.html#.4F.FA.21.55.58.27.F8.31.85.2D.01.55.89.10.A7.16)。

### 设置指定用户对Bucket的访问权限

BOS还可以实现设置指定用户对Bucket的访问权限，参考如下代码实现：

```go
// import "github.com/baidubce/bce-sdk-go/bce"
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

// 1. 直接上传ACL文件流
aclBodyStream := bce.NewBodyFromFile("<path-to-acl-file>")
err := bosClient.PutBucketAcl(bucket, aclBodyStream)

// 2. 直接使用ACL json字符串
aclString := `{
    "accessControlList":[
        {
            "grantee":[{
                "id":"e13b12d0131b4c8bae959df4969387b8" //指定用户ID
            }],
            "permission":["FULL_CONTROL"] //指定用户权限
        }
    ]
}`
err := bosClient.PutBucketAclFromString(bucket, aclString)

// 3. 使用ACL文件
err := bosClient.PutBucketAclFromFile(bucket, "<acl-file-name>")

// 4. 使用ACL struct对象设置
grantUser1 := api.GranteeType{"<user-id-1>"}
grantUser2 := api.GranteeType{"<user-id-2>"}
grant1 := api.GrantType{
	Grantee: []api.GranteeType{grantUser1},
	Permission: []string{"FULL_CONTROL"}
}
grant2 := api.GrantType{
	Grantee: []api.GranteeType{granteUser2},
	Permission: []string{"READ"}
}
grantArr := make([]api.GranteType)
grantArr = append(grantArr, grant1)
grantArr = append(grantArr, grant2)
args := &api.PutBucketAclArgs{grantArr}
err := bosClient.PutBucketAclFromStruct(bucketName, args)
```

> **注意：**
> Permission中的权限设置包含三个值：`READ`、`WRITE`、`FULL_CONTROL`，它们分别对应相关权限。具体内容可以参考BOS API文档 [上传ACL文件方式的权限控制](https://cloud.baidu.com/doc/BOS/API.html#.D4.56.61.2C.A5.B1.68.B6.42.32.3E.18.15.BD.CE.43)。
> ACL规则比较复杂，直接编辑ACL的文件或JSON字符串比较困难，因此提供了第四种方式方便使用代码创建ACL规则。

### 设置更多Bucket访问权限

1. 通过设置referer白名单方式设置防盗链

```go
aclString := `{
    "accessControlList":[
        {
            "grantee":[{"id":"*"]},        //指定用户ID为全部用户
            "permission":["FULL_CONTROL"], //指定用户权限
            "condition":[{"referer": {"stringEquals": "http://allowed-domain/"}}]
        }
    ]
}`
err := bosClient.PutBucketAclFromString(bucket, aclString)
```

2. 限制客户端IP访问，只允许部分客户端IP访问

```go
aclString := `{
    "accessControlList":[
        {
            "grantee":[{"id":"*"]}, //指定用户ID为全部用户
            "permission":["READ"],  //指定用户权限
            "condition":[{"ipAddress": ["ip-1", "ip-2"]}]
        }
    ]
}`
err := bosClient.PutBucketAclFromString(bucket, aclString)
```

### 设置STS临时token权限

对于通过STS方式创建的临时访问身份，管理员也可进行专门的权限设定。

STS的简介及设置临时权限的方式可参见[临时授权访问](https://cloud.baidu.com/doc/BOS/API.html#.E4.B8.B4.E6.97.B6.E6.8E.88.E6.9D.83.E8.AE.BF.E9.97.AE)。

使用BOS GO SDK设置STS临时token权限可参考如下示例：

```go
// import "github.com/baidubce/bce-sdk-go/services/sts"

AK, SK := <your-access-key-id>, <your-secret-access-key>
stsClient, err := sts.NewClient(AK, SK)
aclString := `{
    "accessControlList":[
        {
            "grantee":[{"id":"*"]},        //指定用户ID为全部用户
            "permission":["FULL_CONTROL"], //指定用户权限
            "condition":[{"referer": {"stringEquals": "http://allowed-domain/"}}]
        }
    ]
}`
//使用有效期为300秒且指定ACL的方式获取临时STS token
sts, err := stsClient.GetSessionToken(300, aclString)
```

## 查看Bucket所属的区域

Bucket Location即Bucket Region，百度云支持的各region详细信息可参见[区域选择说明](https://cloud.baidu.com/doc/Reference/Regions.html)。

如下代码可以获取该Bucket的Location信息：

```go
location, err := bosClient.GetBucketLocation(bucketName)
```

## 新建Bucket

如下代码可以新建一个Bucket：

```go
// 新建Bucket的接口为PutBucket，需指定Bucket名称
if loc, err := bosClient.PutBucket(<your-bucket-name>); err != nil {
	fmt.Println("create bucket failed:", err)
} else {
	fmt.Println("create bucket success at location:", loc)
}
```

> **注意：** 由于Bucket的名称在所有区域中是唯一的，所以需要保证bucketName不与其他所有区域上的Bucket名称相同。
>
> Bucket的命名有以下规范：
> - 只能包括小写字母，数字，短横线（-）。
> - 必须以小写字母或者数字开头。
> - 长度必须在3-63字节之间。

## 列举Bucket

如下代码可以列出用户所有的Bucket：

```go
if res, err := bosClient.ListBuckets(); err != nil {
	fmt.Println("list buckets failed:", err)
} else {
	fmt.Println("owner:", res.Owner)
	for i, b := range res.Buckets {
		fmt.Println("bucket", i)
		fmt.Println("    Name:", b.Name)
		fmt.Println("    Location:", b.Location)
		fmt.Println("    CreationDate:", b.CreationDate)
	}
}
```

## 删除Bucket

如下代码可以删除一个Bucket：

```go
err := bosClient.DeleteBucket(bucketName)
```

> **注意：**
> - 在删除前需要保证此Bucket下的所有Object已经已被删除，否则会删除失败。
> - 在删除前确认该Bucket没有开通跨区域复制，不是跨区域复制规则中的源Bucket或目标Bucket，否则不能删除。

## 判断Bucket是否存在

若用户需要判断某个Bucket是否存在，则如下代码可以做到：

```go
exists, err := bosClient.DoesBucketExist(bucketName)
if err == nil && exists {
	fmt.Println("Bucket exists")
} else {
	fmt.Println("Bucket not exists")
}
```


> **注意：**
> 如果Bucket不为空（即Bucket中有Object存在），则Bucket无法被删除，必须清空Bucket后才能成功删除。


# 文件管理

## 上传文件

在BOS中，用户操作的基本数据单元是Object。Object包含Key、Meta和Data。其中，Key是Object的名字；Meta是用户对该Object的描述，由一系列Name-Value对组成；Data是Object的数据。

BOS GO SDK提供了丰富的文件上传接口，可以通过以下方式上传文件：

- 简单上传
- 追加上传
- 抓取上传
- 分块上传

### 简单上传

BOS在简单上传的场景中，支持以指定文件形式、以数据流方式、以二进制串方式、以字符串方式执行Object上传，请参考如下代码：

```go
// import "github.com/baidubce/bce-sdk-go/bce"

// 从本地文件上传
etag, err := bosClient.PutObjectFromFile(bucketName, objectName, fileName, nil)

// 从字符串上传
str := "test put object"
etag, err := bosClient.PutObjectFromString(bucketName, objectName, str, nil)

// 从字节数组上传
byteArr := []byte("test put object")
etag, err := bosClient.PutObjectFromBytes(bucketName, objectName, byteArr, nil)

// 从数据流上传
bodyStream, err := bce.NewBodyFromFile(fileName)
etag, err := bosClient.PutObject(bucketName, objectName, bodyStream, nil)

// 使用基本接口，提供必需参数从数据流上传
bodyStream, err := bce.NewBodyFromFile(fileName)
etag, err := bosClient.BasicPutObject(bucketName, objectName, bodyStream)
```

Object以文件的形式上传到BOS中，上述简单上传的接口支持不超过5GB的Object上传。在请求处理成功后，BOS会在Header中返回Object的ETag作为文件标识。

**设置文件元信息**

文件元信息(Object Meta)，是对用户在向BOS上传文件时，同时对文件进行的属性描述，主要分为分为两种：设置HTTP标准属性（HTTP Headers）和用户自定义的元信息。

***设定Object的Http Header***

BOS GO SDK本质上是调用后台的HTTP接口，因此用户可以在上传文件时自定义Object的Http Header。常用的http header说明如下：

名称 | 描述 |默认值
---|---|---
Content-MD5 | 文件数据校验，设置后BOS会启用文件内容MD5校验，把您提供的MD5与文件的MD5比较，不一致会抛出错误 | 有
Content-Type | 文件的MIME，定义文件的类型及网页编码，决定浏览器将以什么形式、什么编码读取文件。如没有指定，BOS则根据文件的扩展名自动生成，如文件没有扩展名则填默认值 | application/octet-stream
Content-Disposition | 指示MIME用户代理如何显示附加的文件，打开或下载，及文件名称 | 无
Content-Length | 上传的文件的长度，超过流/文件的长度会截断，不足为实际值 | 流/文件的长度
Expires| 缓存过期时间 | 无
Cache-Control | 指定该Object被下载时的网页的缓存行为 | 无

参考代码如下：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.PutObjectArgs)

// 设置上传内容的MIME类型
args.ContentType = "text/javascript"

// 设置上传内容的长度
args.ContentLength = 1024

// 设置缓存过期时间
args.Expires = "Mon, 19 Mar 2018 11:55:32 GMT"

// 设置缓存行为
args.CacheControl = "max-age=3600"

etag, err := bosClient.PutObject(bucketName, objectName, bodyStream, args)
```

> 注意：用户上传对象时SDK会自动设置ContentLength和ContentMD5，用来保证数据的正确性。如果用户自行设定ContentLength，必须为大于等于0且小于等于实际对象大小的数值，从而上传截断部分的内容，为负数或大于实际大小均报错。

***用户自定义元信息***

BOS支持用户自定义元数据来对Object进行描述。如下代码所示：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.PutObjectArgs)

// 设置用户自定义元数据
args.UserMeta = map[string]string{
    "name1": "my-metadata1",
    "name2": "my-metadata2",
}

etag, err := bosClient.PutObject(bucketName, objectName, bodyStream, args)
```

> **提示：**
> - 在上面代码中，用户自定义了一个名字为“name1”和“name2”，值分别为“my-metadata1”和“my-metadata2”的元数据
> - 当用户下载此Object的时候，此元数据也可以一并得到
> - 一个Object可以有多个类似的参数，但所有的User Meta总大小不能超过2KB

**上传Object时设置存储类型**

BOS支持标准存储、低频存储和冷存储，上传Object并存储为低频存储类型通过指定StorageClass实现，三种存储类型对应的参数如下：

存储类型 | 参数
---|---
标准存储 | STANDRAD
低频存储 | STANDARD_IA
冷存储 | COLD

以低频存储为例，代码如下：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.PutObjectArgs)
args.StorageClass = api.STORAGE_CLASS_STANDARD_IA
etag, err := bosClient.PutObject(bucketName, objectName, bodyStream, args)
```

### 追加上传

上文介绍的简单上传方式，创建的Object都是Normal类型，用户不可再进行追加写，这在日志、视频监控、视频直播等数据复写较频繁的场景中使用不方便。

正因如此，百度云BOS特别支持了AppendObject，即以追加写的方式上传文件。通过AppendObject操作创建的Object类型为Appendable Object，可以对该Object追加数据。AppendObject大小限制为0~5G。当您的网络情况较差时，推荐使用AppendObject的方式进行上传，每次追加较小数据（如256kb）。

通过AppendObject方式上传示例代码如下：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.AppendObjectArgs)

// 1. 原始接口上传，设置为低频存储，设置追加的偏移位置
args.StorageClass = api.STORAGE_CLASS_STANDARD_IA
args.Offset = 1024
res, err := bosClient.AppendObject(bucketName, objectName, bodyStream, args)

// 2. 封装的简单接口，仅支持设置offset
res, err := bosClient.SimpleAppendObject(bucketName, objectName, bodyStream, offset)

// 3. 封装的从字符串上传接口，仅支持设置offset
res, err := bosClient.SimpleAppendObjectFromString(bucketName, objectName, "abc", offset)

// 4. 封装的从给出的文件名上传文件的接口，仅支持设置offset
res, err := bosClient.SimpleAppendObjectFromFile(bucketName, objectName, "<path-to-local-file>", offset)

fmt.Println(res.ETag)             // 打印ETag
fmt.Println(res.ContentMD5)       // 打印ContentMD5
fmt.Println(res.NextAppendOffset) // 打印NextAppendOffset
```

### 抓取上传

BOS支持用户提供的url自动抓取相关内容并保存为指定Bucket的指定名称的Object。

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.FetchObjectArgs)

// 1. 原始接口抓取，设置为异步抓取模式
args.FetchMode = api.FETCH_MODE_ASYNC
res, err := bosClient.FetchObject(bucket, object, url, args)

// 2. 基本抓取接口，默认为同步抓取模式
res, err := bosClient.BasicFetchObject(bucket, object, url)

// 3. 易用接口，直接指定可选参数
res, err := bosClient.SimpleFetchObject(bucket, object, url,
	api.FETCH_MODE_ASYNC, api.STORAGE_CLASS_STANDARD_IA)

fmt.Println(res.ETag) // 打印ETag
```

### 分块上传

除了通过简单上传几追加上传方式将文上传件到BOS以外，BOS还提供了另外一种上传模式 —— Multipart Upload。用户可以在如下的应用场景内（但不仅限于此），使用Multipart Upload上传模式，如：

- 需要支持断点上传。
- 上传超过5GB大小的文件。
- 网络条件较差，和BOS的服务器之间的连接经常断开。
- 需要流式地上传文件。
- 上传文件之前，无法确定上传文件的大小。

BOS GO SDK提供了分块操作的控制参数：

- MultipartSize：每个分块的大小，默认为10MB，最小不得低于5MB
- MaxParallel：分块操作的并发数，默认为10

下面的示例代码设置了分块的大小为20MB，并发数为100：

```
// import "github.com/baidubce/bce-sdk-go/services/bos"

client := bos.NewClient(<your-ak>, <your-sk>, <endpoint>)
client.MultipartSize = 20 * (1 << 10)
client.MaxParallel = 100
```

除了上述参数外，还会对设置的每个分块数进行1MB对齐，同时限制是最大分块数目不得超过10000，如果分块较小导致分块数超过这个上限会自动调整分块大小。

下面将一步步介绍Multipart Upload的实现。假设有一个文件，本地路径为 `/path/to/file.zip`，由于文件比较大，将其分块传输到BOS中。

**初始化Multipart Upload**

使用`BasicInitiateMultipartUpload`方法来初始化一个基本的分块上传事件：

```go
res, err := bosClient.BasicInitiateMultipartUpload(bucketName, objectKey)
fmt.Println(res.UploadId) // 打印初始化分块上传后获取的UploadId
```

返回结果中含有 `UploadId` ，它是区分分块上传事件的唯一标识，在后面的操作中，我们将用到它。

***上传低频存储类型Object的初始化***

BOS GO SDK提供的`InitiateMultipartUpload`接口可以设置其他分块上传的相关参数，下面的代码初始化了低频存储的一个分块上传事件：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.InitiateMultipartUploadArgs)
args.StorageClass = api.STORAGE_CLASS_STANDARD_IA
res, err := bosClient.InitiateMultipartUpload(bucketName, objectKey, contentType, args)
fmt.Println(res.UploadId) // 打印初始化分块上传后获取的UploadId
```

***上传冷存储类型Object的初始化***

初始化低频存储的一个分块上传事件：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.InitiateMultipartUploadArgs)
args.StorageClass = api.STORAGE_CLASS_COLD
res, err := bosClient.InitiateMultipartUpload(bucketName, objectKey, contentType, args)
fmt.Println(res.UploadId) // 打印初始化分块上传后获取的UploadId
```

**上传分块**

接着，把文件分块上传。

```go
// import "github.com/baidubce/bce-sdk-go/bce"
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

file, _ := os.Open("/path/to/file.zip")

// 分块大小按MULTIPART_ALIGN=1MB对齐
partSize := (bosClient.MultipartSize +
	bce.MULTIPART_ALIGN - 1) / bce.MULTIPART_ALIGN * bce.MULTIPART_ALIGN

// 获取文件大小，并计算分块数目，最大分块数MAX_PART_NUMBER=10000
fileInfo, _ := file.Stat()
fileSize := fileInfo.Size()
partNum := (fileSize + partSize - 1) / partSize
if partNum > bce.MAX_PART_NUMBER { // 超过最大分块数，需调整分块大小
	partSize := (fileSize + bce.MAX_PART_NUMBER + 1) / bce.MAX_PART_NUMBER
	partSize := (partSize + bce.MULTIPART_ALIGN - 1) / bce.MULTIPART_ALIGN * bce.MULTIPART_ALIGN
	partNum = (fileSize + partSize - 1) / partSize
}

// 创建保存每个分块上传后的ETag和PartNumber信息的列表
partEtags := make([]api.UploadInfoType)

// 逐个分块上传
for i := int64(1); i <= partNum; i++  {
	// 计算偏移offset和本次上传的大小uploadSize
	uploadSize := partSize
	offset := partSize * (i - 1)
	left := fileSize - offset
	if left < partSize {
		uploadSize = left
	}

	// 创建指定偏移、指定大小的文件流
	partBody, _ := bce.NewBodyFromSectionFile(file, offset[i], uploadSize)

	// 上传当前分块
	etag, err := bosClient.BasicUploadPart(bucketName, objectKey, uploadId, i, partBody)

	// 保存当前分块上传成功后返回的序号和ETag
	partEtags = append(partEtags, api.UploadInfoType{partNum, etag})
}
```

上面代码的核心是调用 `BasicUploadPart` 方法来上传每一个分块，但是要注意以下几点：

- BasicUploadPart 方法要求除最后一个Part以外，其他的Part大小都要大于等于5MB。但是该接口并不会立即校验上传Part的大小；只有当Complete Multipart Upload的时候才会校验。
- 为了保证数据在网络传输过程中不出现错误，建议您在`BasicUploadPart`后，使用每个分块BOS返回的Content-MD5值分别验证已上传分块数据的正确性。当所有分块数据合成一个Object后，不再含MD5值。
- Part号码的范围是1~10000。如果超出这个范围，BOS将返回InvalidArgument的错误码。
- 每次上传Part之后，BOS的返回结果会包含一个 `PartETag`对象，它是上传块的ETag与块编号（PartNumber）的组合，在后续完成分块上传的步骤中会用到它，因此需要将其保存起来。一般来讲这些`PartETag` 对象将被保存到List中。

**完成分块上传**

如下代码所示，完成分块上传：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

completeArgs := &api.CompleteMultipartUploadArgs{partEtags}
res, _ := bosClient.CompleteMultipartUploadFromStruct(
	bucketName, objectKey, uploadId, completeArgs, nil)

// 输出结果对象的内容
fmt.Println(res.Location)
fmt.Println(res.Bucket)
fmt.Println(res.Key)
fmt.Println(res.ETag)
```

上面代码中的 `partETags`是第二部中保存的partETag的列表，BOS收到用户提交的Part列表后，会逐一验证每个数据Part的有效性。当所有的数据Part验证通过后，BOS将把这些数据part组合成一个完整的Object。

**取消分块上传**

用户可以使用abortMultipartUpload方法取消分块上传。

```go
bosClient.AbortMultipartUpload(bucketName, objectKey, uploadId)
```

**获取未完成的分块上传**

用户可以使用 `ListMultipartUploads` 方法获取Bucket内未完成的分块上传事件。

```go
// 列出给定bucket下所有未完成的分块信息
res, err := BasicListMultipartUploads(bucketName)

// 输出返回结果状态信息
fmt.Println(res.Bucket)
fmt.Println(res.Delimiter)
fmt.Println(res.Prefix)
fmt.Println(res.IsTruncated)
fmt.Println(res.KeyMarker)
fmt.Println(res.NextKeyMarker)
fmt.Println(res.MaxUploads)

// 遍历所有未完成分块信息列表
for _, multipartUpload := range res.Uploads {
	fmt.Println("Key:", multipartUpload.Key, ", UploadId:", multipartUpload.UploadId)
}
```

> **注意：**
> 1. 默认情况下，如果Bucket中的分块上传事件的数目大于1000，则只会返回1000个Object，并且返回结果中IsTruncated的值为True，同时返回NextKeyMarker作为下次读取的起点。
> 2. 若想返回更多分块上传事件的数目，可以使用KeyMarker参数分次读取。

**获取所有已上传的块信息**

用户可以使用 `ListParts` 方法获取某个上传事件中所有已上传的块。

```go
// 使用基本接口列出当前上传成功的分块
res, err := bosClient.BasicListParts(bucketName, objectKey, uploadId)

// 使用原始接口提供参数，列出当前上传成功的最多100个分块
args := new(api.ListPartsArgs)
args.MaxParts = 100
res, err := bosClient.ListParts(bucketName, objectKey, uploadId, args)

// 打印返回的状态结果
fmt.Println(res.Bucket)
fmt.Println(res.Key)
fmt.Println(res.UploadId)
fmt.Println(res.Initiated)
fmt.Println(res.StorageClass)
fmt.Println(res.PartNumberMarker)
fmt.Println(res.NextPartNumberMarker)
fmt.Println(res.MaxParts)
fmt.Println(res.IsTruncated)

// 打印分块信息
for _, part := range res.Parts {
	fmt.Println("PartNumber:", part.PartNumber, ", Size:", part.Size,
		", ETag:", part.ETag, ", LastModified:", part.LastModified)
}
```

> **注意：**
> 1. 默认情况下，如果Bucket中的分块上传事件的数目大于1000，则只会返回1000个Object，并且返回结果中IsTruncated的值为True，同时返回NextPartNumberMarker作为下次读取的起点。
> 2. 若想返回更多分块上传事件的数目，可以使用PartNumberMarker参数分次读取。

上述示例是使用API依次实现，没有并发执行，如果需要加快速度需要用户实现并发上传的部分。为了方便用户使用，BOS Client特封装了分块上传的并发接口`UploadSuperFile`：

- 接口：`UploadSuperFile(bucket, object, fileName, storageClass string) error`
- 参数:
    - bucket: 上传对象的bucket的名称
    - object: 上传对象的名称
    - fileName: 本地文件名称
    - storageClass: 上传对象的存储类型，默认标准存储
- 返回值:
    - error: 上传过程中的错误，成功则为空

用户只需给出`bucket`、`object`、`filename`即可并发的进行分块上传，同时也可指定上传对象的`storageClass`。

## 下载文件

BOS GO SDK提供了丰富的文件下载接口，用户可以通过以下方式从BOS中下载文件：

- 简单流式下载
- 下载到本地文件
- 范围下载

### 简单流式下载

用户可以通过如下代码将Object读取到一个流中：

```go
// 提供Bucket和Object，直接获取一个对象
res, err := bosClient.BasicGetObject(bucketName, objectName)

// 获取ObjectMeta
meta := res.ObjectMeta

// 获取Object的读取流（io.ReadCloser）
stream := res.Body

// 确保关闭Object读取流
defer stream.Close()

// 调用stream对象的Read方法处理Object
...
```

> **注意：**
> 1. 上述接口的返回结果对象中包含了Object的各种信息，包含Object所在的Bucket、Object的名称、MetaData以及一个读取流。
> 2. 可通过结果对象的ObjectMeta字段获取对象的元数据，它包含了Object上传时定义的ETag，Http Header以及自定义的元数据。
> 3. 可通过结果对象的Body字段获取返回Object的读取流，通过操作读取流将Object的内容读取到文件或者内存中或进行其他操作。

### 下载到本地文件

用户可以通过如下代码直接将Object下载到指定文件：

```go
err := bosClient.BasicGetObjectToFile(bucketName, objectName, "path-to-local-file")
```

### 范围下载

为了实现更多的功能，可以指定下载范围、返回header来实现更精细化地获取Object。如果指定的下载范围是0 - 100，则返回第0到第100个字节的数据，包括第100个，共101字节的数据，即[0, 100]。

```go
// 指定范围起始位置和返回header
responseHeaders := map[string]string{"ContentType": "image/gif"}
rangeStart = 1024
rangeEnd = 2048
res, err := bosClient.GetObject(bucketName, objectName, responseHeaders, rangeStart, rangeEnd)

// 只指定起始位置start
res, err := bosClient.GetObject(bucketName, objectName, responseHeaders, rangeStart)

// 不指定range
res, err := bosClient.GetObject(bucketName, objectName, responseHeaders)

// 不指定返回可选头部
res, err := bosClient.GetObject(bucketName, objectName, nil)
```

基于范围下载接口，用户可以据此实现文件的分段下载和断点续传。为了方便用户使用，BOS GO SDK封装了并发下载的接口`DownloadSuperFile`：

- 接口：`DownloadSuperFile(bucket, object, fileName string) error`
- 参数:
    - bucket: 下载对象所在bucket的名称
    - object: 下载对象的名称
    - fileName: 该对象保存到本地的文件名称
- 返回值:
    - error: 下载过程中的错误，成功则为空

该接口利用并发控制参数执行并发范围下载，直接下载到用户指定的文件中。

### 其他使用方法

**获取Object的存储类型**

Object的storage class属性分为`STANDARD`(标准存储)、`STANDARD_IA`(低频存储)和`COLD`(冷存储)，通过如下代码可以实现：

```go
res, err := bosClient.GetObjectMeta(bucketName, objectName)
fmt.Println(res.StorageClass)
```

**只获取Object Metadata**

通过GetObjectMeta方法可以只获取Object Metadata而不获取Object的实体。如下代码所示：

```go
res, err := bosClient.GetObjectMeta(bucketName, objectName)
fmt.Printf("Metadata: %+v\n", res)
```

## 获取文件下载URL

用户可以通过如下代码获取指定Object的URL：

```go
// 1. 原始接口，可设置bucket、object名称，过期时间、请求方法、请求头和请求参数
url := bosClient.GeneratePresignedUrl(bucketName, objectName,
		expirationInSeconds, method, headers, params)

// 2. 基本接口，默认为`GET`方法，仅需设置过期时间
url := bosClient.BasicGeneratePresignedUrl(bucketName, objectName, expirationInSeconds)
```

> **说明：**
>
> * 用户在调用该函数前，需要手动设置endpoint为所属区域域名。百度云目前开放了多区域支持，请参考[区域选择说明](https://cloud.baidu.com/doc/Reference/Regions.html)。目前支持“华北-北京”、“华南-广州”和“华东-苏州”三个区域。北京区域：`http://bj.bcebos.com`，广州区域：`http://gz.bcebos.com`，苏州区域：`http://su.bcebos.com`。
> * `expirationInSeconds`为指定的URL有效时长，时间从当前时间算起，为可选参数，不配置时系统默认值为1800秒。如果要设置为永久不失效的时间，可以将`expirationInSeconds`参数设置为-1，不可设置为其他负数。
> * 如果预期获取的文件时公共可读的，则对应URL链接可通过简单规则快速拼接获取: http://{$bucketName}.{$region}.bcebos.com/{$objectName}。

## 列举存储空间中的文件

BOS GO SDK支持用户通过以下两种方式列举出object：

- 简单列举
- 通过参数复杂列举

除此之外，用户还可在列出文件的同时模拟文件夹。

### 简单列举

当用户希望简单快速列举出所需的文件时，可通过ListObjects方法返回ListObjectsResult对象，ListObjectsResult对象包含了此次请求的返回结果。用户可以从ListObjectsResult对象的Contents字段获取Object的所有描述信息。

```go
listObjectResult, err := bosClient.ListObjects(bucketName, nil)

// 打印当前ListObjects请求的状态结果
fmt.Println("Name:", listObjectResult.Name)
fmt.Println("Prefix:", listObjectResult.Prefix)
fmt.Println("Delimiter:", listObjectResult.Delimiter)
fmt.Println("Marker:", listObjectResult.Marker)
fmt.Println("NextMarker:", listObjectResult.NextMarker)
fmt.Println("MaxKeys:", listObjectResult.MaxKeys)
fmt.Println("IsTruncated:", listObjectResult.IsTruncated)

// 打印Contents字段的具体结果
for _, obj := range listObjectResult.Contents {
	fmt.Println("Key:", obj.Key, ", ETag:", obj.ETag, ", Size:", obj.Size,
		", LastModified:", obj.LastModified, ", StorageClass:", obj.StorageClass)
}
```

> **注意：**
> 1. 默认情况下，如果Bucket中的Object数量大于1000，则只会返回1000个Object，并且返回结果中IsTruncated值为True，并返回NextMarker做为下次读取的起点。
> 2. 若想增大返回Object的数目，可以使用Marker参数分次读取。

### 通过参数复杂列举

除上述简单列举外，用户还可通过设置ListObjectsArgs参数实现各种灵活的查询功能。ListObjectsArgs可设置的参数如下：

参数 | 功能
-----|-----
Prefix | 限定返回的object key必须以prefix作为前缀
Delimiter | 分隔符，是一个用于对Object名字进行分组的字符所有名字包含指定的前缀且第一次出现。Delimiter字符之间的Object作为一组元素
Marker | 设定结果从marker之后按字母排序的第一个开始返回
MaxKeys | 限定此次返回object的最大数，如果不设定，默认为1000，max-keys取值不能大于1000

> **注意：**
> 1. 如果有Object以Prefix命名，当仅使用Prefix查询时，返回的所有Key中仍会包含以Prefix命名的Object，详见[递归列出目录下所有文件](#递归列出目录下所有文件)。
> 2. 如果有Object以Prefix命名，当使用Prefix和Delimiter组合查询时，返回的所有Key中会有Null，Key的名字不包含Prefix前缀，详见[查看目录下的文件和子目录](#查看目录下的文件和子目录)。

下面我们分别以几个案例说明通过参数列举的方法：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.ListObjectsArgs)

// 指定最大返回参数为500
args.MaxKeys = 500

// 指定满足特定前缀
args.Prefix = "my-prefix/"

// 指定分隔符，实现类似文件夹的功能
args.Delimiter = "/"

// 设置特定Object之后的排序结果
args.Marker = "bucket/object-0"

listObjectResult, err := bosClient.ListObjects(bucketName, args)
```

### 模拟文件夹功能

在BOS的存储结果中是没有文件夹这个概念的，所有元素都是以Object来存储，但BOS的用户在使用数据时往往需要以文件夹来管理文件。因此，BOS提供了创建模拟文件夹的能力，其本质上来说是创建了一个size为0的Object。对于这个Object可以上传下载，只是控制台会对以“/”结尾的Object以文件夹的方式展示。

用户可以通过Delimiter和Prefix参数的配合模拟出文件夹功能。Delimiter和Prefix的组合效果是这样的：

如果把Prefix设为某个文件夹名，就可以罗列以此Prefix开头的文件，即该文件夹下递归的所有的文件和子文件夹（目录）。文件名在Contents中显示。
如果再把 Delimiter 设置为“/”时，返回值就只罗列该文件夹下的文件和子文件夹（目录），该文件夹下的子文件名（目录）返回在CommonPrefixes 部分，子文件夹下递归的文件和文件夹不被显示。

如下是几个应用方式：

**列出Bucket内所有文件**

当用户需要获取Bucket下的所有文件时，可以参考如下代码：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.ListObjectsArgs)
args.Delimiter = "/"
listObjectResult, err := bosClient.ListObjects(bucketName, args)
```

**递归列出目录下所有文件**

可以通过设置 `Prefix` 参数来获取某个目录下所有的文件：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.ListObjectsArgs)
args.Prefix = "fun/"
listObjectResult, err := bosClient.ListObjects(bucketName, args)
fmt.Println("Objects:")
for _, obj := range listObjectResult.Contents {
	fmt.Println(obj.Key)
}
```

输出：

    Objects:
    fun/
    fun/movie/001.avi
    fun/movie/007.avi
    fun/test.jpg

**查看目录下的文件和子目录**

在 `Prefix` 和 `Delimiter` 结合的情况下，可以列出目录下的文件和子目录：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.ListObjectsArgs)
args.Delimiter = "/"
args.Prefix = "fun/"
listObjectResult, err := bosClient.ListObjects(bucketName, args)

// 遍历所有的Objects（当前目录和直接子文件）
fmt.Println("Objects:")
for _, obj := range listObjectResult.Contents {
	fmt.Println(obj.Key)
}

// 遍历所有的CommonPrefix（子目录）
fmt.Println("CommonPrefixs:")
for _, obj := range listObjectResult.CommonPrefixes {
	fmt.Println(obj.Prefix)
}
```

输出：
    Objects:
    fun/
    fun/test.jpg
    
    CommonPrefixs:
    fun/movie/


返回的结果中，`ObjectSummaries` 的列表中给出的是fun目录下的文件。而`CommonPrefixs`的列表中给出的是fun目录下的所有子文件夹。可以看出`fun/movie/001.avi` ，`fun/movie/007.avi`两个文件并没有被列出来，因为它们属于 `fun` 文件夹下的 `movie` 目录。

### 列举Bucket中object的存储属性

当用户完成上传后，如果需要查看指定Bucket中的全部Object的storage class属性，可以通过如下代码实现：

```go
listObjectResult, err := bosClient.ListObjects(bucketName, args)
for _, obj := range listObjectResult.Contents {
	fmt.Println("Key:", obj.Key)
	fmt.Println("LastModified:", obj.LastModified)
	fmt.Println("ETag:", obj.ETag)
	fmt.Println("Size:", obj.Size)
	fmt.Println("StorageClass:", obj.StorageClass)
	fmt.Println("Owner:", obj.Owner.Id, obj.Owner.DisplayName)
}
```

## 删除文件

**删除单个文件**

可参考如下代码删除了一个Object:

```go
// 指定要删除Object名称和所在的Bucket名称
err := bosClient.DeleteObject(bucketName, objectName)
```

**删除多个文件**

用户也可通过一次调用删除同一个Bucket下的多个文件，有如下参数：

参数名称 | 描述    | 父节点
---------|---------|--------
objects  | 保存要删除的Object信息的容器，包含一个或多个Object元素 | -
+key     | 要删除的Object的名称 | objects

具体示例如下：

```
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

// 1. 原始接口，提供多个Object的List Stream
res, err := bosClient.DeleteMultipleObjects(bucket, objectListStream)

// 2. 提供json字符串删除
objectList := `{
    "objects":[
        {"key": "aaa"},
        {"key": "bbb"}
    ]
}`
res, err := bosClient.DeleteMultipleObjectsFromString(bucket, objectList)

// 3. 提供删除Object的List对象
deleteObjectList := make([]api.DeleteObjectArgs, 0)
deleteObjectList = append(deleteObjectList, api.DeleteObjectArgs{"aaa"})
deleteObjectList = append(deleteObjectList, api.DeleteObjectArgs{"bbb"})
multiDeleteObj := &api.DeleteMultipleObjectsArgs{deleteObjectList}
res, err := bosClient.DeleteMultipleObjectsFromStruct(bucket, multiDeleteObj)

// 4. 直接提供待删除Object的名称列表
deleteObjects := []string{"aaa", "bbb"}
res, err := bosClient.DeleteMultipleObjectsFromKeyList(bucket, deleteObjects)
```

> **说明：**
>
> 一次删除多个Object的时候，返回的结果里包含了未删除成功的Object名称列表。删除部分对象成功时`res`里包含了未删除成功的名称列表。
> 删除部分对象成功时`err`为`nil`且`res`不为`nil`，判断全部删除成功：`err`为`io.EOF`且`res`为`nil`。

## 查看文件是否存在

用户可通过如下操作查看某文件是否存在：

```go
// import "github.com/baidubce/bce-sdk-go/bce"

_, err := bosClient.GetObjectMeta(bucketName, objectName)
if realErr, ok := err.(*bce.BceServiceError); ok {
	if realErr.StatusCode == 404 {
		fmt.Println("object not exists")
	}
}
fmt.Println("object exists")
```

## 获取及更新文件元信息

文件元信息(Object Metadata)，是对用户上传BOS的文件的属性描述，分为两种：HTTP标准属性（HTTP Headers）和User Meta（用户自定义元信息）。

### 获取文件元信息

用户通过GetObjectMeta方法可以只获取Object Metadata而不获取Object的实体。如下代码所示：

```go
res, err := bosClient.GetObjectMeta(bucketName, objectName)
fmt.Printf("Metadata: %+v\n", res)
```

### 修改文件元信息

BOS修改Object的Metadata通过拷贝Object实现。即拷贝Object的时候，把目的Bucket设置为源Bucket，目的Object设置为源Object，并设置新的Metadata，通过拷贝自身实现修改Metadata的目的。如果不设置新的Metadata，则报错。这种方式下必须使用拷贝模式为“replace”（默认情况为“copy”）。示例如下：

```go
// import "github.com/baidubce/bce-sdk-go/bce"

args := new(api.CopyObjectArgs)

// 必须设置拷贝模式为"replace"，默认为"copy"是不能执行Metadata修改的
args.MetadataDirective="replace"

// 设置Metadata参数值，具体字段请参考官网说明
args.LastModified = "Wed, 29 Nov 2017 13:18:08 GMT"
args.ContentType = "text/json"

// 使用CopyObject接口修改Metadata，源对象和目的对象相同
res, err := bosClient.CopyObject(bucket, object, bucket, object, args)
```

## 拷贝文件

### 拷贝一个文件

用户可以通过CopyObject方法拷贝一个Object，如下代码所示：

```go
// 1. 原始接口，可设置拷贝参数
res, err := bosClient.CopyObject(bucketName, objectName, srcBucket, srcObject, nil)

// 2. 忽略拷贝参数，使用默认
res, err := bosClient.BasicCopyObject(bucketName, objectName, srcBucket, srcObject)

fmt.Println("ETag:", res.ETag, "LastModified:", res.LastModified)
```

上述接口返回的结果对象中包含了新Object的ETag和修改时间LastModified。

### 设置拷贝参数拷贝Object

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.CopyObjectArgs)

// 设置用户自定义Metadata
args.UserMeta = map[string]string{"<user-meta-key>": "<user-meta-value>"}

res, err := bosClient.CopyObject(bucketName, objectName, srcBucket, srcObject, args)
fmt.Println("ETag:", res.ETag, "LastModified:", res.LastModified)
```

**设置Object的Copy属性**

用户在执行拷贝的过程中，可以对源Object的Etag或修改状态进行判断，根据判断结果决定是否执行拷贝。详细的参数解释如下：

| 名称 | 类型 | 描述 | 是否必需 |
| --- | --- | --- | ---- | 
| x-bce-copy-source-if-match | String | 如果源Object的ETag值和用户提供的ETag相等，则执行拷贝操作，否则拷贝失败。 | 否 |
| x-bce-copy-source-if-none-match | String | 如果源Object的ETag和用户提供的ETag不相等，则执行拷贝操作，否则拷贝失败。 | 否 |
| x-bce-copy-source-if-unmodified-since | String | 如果源object在x-bce-copy-source-if-unmodified-since之后没被修改，则执行拷贝操作，否则拷贝失败。 | 否 |
| x-bce-copy-source-if-modified-since | String | 如果源object在x-bce-copy-source-if-modified-since之后被修改了，则执行拷贝操作，否则拷贝失败。 | 否 |

对应的示例代码：

```go
// import "github.com/baidubce/bce-sdk-go/services/bos/api"

args := new(api.CopyObjectArgs)

// 设置用户自定义Metadata
args.UserMeta = map[string]string{"<user-meta-key>": "<user-meta-value>"}

// 设置copy-source-if-match
args.IfMatch = "111111111183bf192b57a4afc76fa632"

// 设置copy-source-if-none-match
args.IfNoneMatch = "111111111183bf192b57a4afc76fa632"

// 设置copy-source-if-modified-since
args.IfModifiedSince = "Fri, 16 Mar 2018 17:07:21 GMT"

// 设置copy-source-if-unmodified-since
args.IfUnmodifiedSince = "Fri, 16 Mar 2018 17:07:21 GMT"

res, err := bosClient.CopyObject(bucketName, objectName, srcBucket, srcObject, args)
fmt.Println("ETag:", res.ETag, "LastModified:", res.LastModified)
```

**同步Copy功能**

当前BOS的CopyObject接口是通过同步方式实现的。同步方式下，BOS端会等待Copy实际完成才返回成功。同步Copy能帮助用户更准确的判断Copy状态，但用户感知的复制时间会变长，且复制时间和文件大小成正比。

同步Copy方式更符合业界常规，提升了与其它平台的兼容性。同步Copy方式还简化了BOS服务端的业务逻辑，提高了服务效率。


# 数据处理及使用

## 生命周期管理

BOS支持用户对Bucket设置生命周期规则，以自动将过期的文件清除，节省存储空间。针对不同前缀的文件，用户可以同时设置多条规则。
在为Bucket设置一条生命周期规则时，需注意如下参数的使用方式：

规则项 |  描述  |  是否必填  |  备注
-------|--------|------------|--------
id | 规则的标识符 | 必填 | 同一个bucket内规则id必须唯一，不能重复。如果用户不填系统会自动帮用户生成一个
status | 规则的状态 |  必填 | 取值为enabled或disabled，当规则处于disabled时规则不生效
resource | 规则对哪些资源生效 | 必填 | 举例：对samplebucket里以prefix/为前缀的Object生效：`samplebucket/prefix/*`
condition | 规则依赖的条件 | 必填 | 目前只支持time形式
+time | 时间限制条件 | 必填 | 通过定义的dateGreaterThan实现
++dateGreaterThan | 描述时间关系 | 必填 | 支持绝对时间date和相对时间days。绝对时间date格式为yyyy-mm-ddThh:mm:ssZ，eg. 2016-09-07T00:00:00Z。绝对时间为UTC时间,必须以00:00:00(UTC 0点)结尾；相对时间days的描述遵循ISO8601,支持的最小时间粒度为天，如:$(lastModified)+P7D表示的时间为object的last-modified之后7天。
action | 对resource执行的操作动作 | 必填 | -
+name  |  执行的操作名称 | 必填 | 取值为Transition、DeleteObject、AbortMultipartUpload
+storageClass | Object的存储类型 | 可选 | action为Transition时可以设定，取值为STANDARD_IA或COLD，表示从原存储类型转为低频存储或冷存储

### 设置生命周期规则

可通过如下代码设置一条生命周期规则：

```go
// import "github.com/baidubce/bce-sdk-go/bce"

ruleStr := `{
    "rule": [
        {
            "id": "delete-rule-1",
            "status": "enabled",
            "resource": ["my-bucket/abc*"],
            "condition": {
                "time": {
                    "dateGreaterThan": "2018-01-01T00:00:00Z"
                }
            },
            "action": {
                "name": "DeleteObject"
            }
        }
    ]
}`

// 1. 通过stream调用接口进行设置
body, _ := bce.NewBodyFromString(ruleStr)
err := bosClient.PutBucketLifecycle(bucketName, body)

// 2. 直接传入字符串
err := bosClient.PutBucketLifecycleFromString(bucketName, ruleStr)
```

### 查看生命周期规则

可通过如下代码查看Bucket内的生命周期规则：

```go
res, err := bosClient.GetBucketLifecycle(bucketName)
fmt.Printf("%+v\n", res.Rule)
```

### 删除生命周期规则

可通过如下代码清空生命周期规则：

```go
err := bosClient.DeleteBucketLifecycle(bucketName)
```

## 管理存储类型

每个Bucket会有自身的存储类型，如果该Bucket下的Object上传时未指定存储类型则会默认继承该Bucket的存储类型。

### 设置Bucket存储类型

Bucket默认的存储类型为标准模式，用户可以通过下面的代码进行设置：

```
storageClass := "STANDARD_IA"
err := bosClient.PutBucketStorageclass(bucketName, storageClass)
```

### 获取Bucket存储类型

下面的代码可以查看一个Bucket的默认存储类型：

```
storageClass, err := bosClient.GetBucketStorageclass(bucketName)
```

## 设置访问日志

BOS GO SDK支持将用户访问Bucket时的请求记录记录为日志，用户可以指定访问Bucket的日志存放的位置。日志会包括请求者、Bucket名称、请求时间和请求操作等。关于Bucket日志的详细功能说明可参见[设置访问日志](https://cloud.baidu.com/doc/BOS/DevRef.html#.E6.97.A5.E5.BF.97.E6.A0.BC.E5.BC.8F)。

### 开启Bucket日志

用户通过设置用于放置日志的Bucket和日志文件前缀来开启Bucket日志功能。下面的示例代码可以设置访问日志的位置和前缀：

```
// import "github.com/baidubce/bce-sdk-go/bce"

// 1. 从JSON字符串设置
loggingStr := `{"targetBucket": "logging-bucket", "targetPrefix": "my-log/"}`
err := bosClient.PutBucketLoggingFromString(bucketName, loggingStr)

// 2. 从参数对象设置
args := new(api.PutBucketLoggingArgs)
args.TargetBucket = "logging-bucket"
args.TargetPrefix = "my-log/"
err := bosClient.PutBucketLoggingFromStruct(bucketName, args)

// 3. 读取json格式的文件进行设置
loggingStrem := bce.NewBodyFromFile("<path-to-logging-setting-file>")
err := bosClient.PutBucketLogging(bucketName, loggingStream)
```

### 查看Bucket日志设置

下面的代码分别给出了如何获取给定Bucket的日志配置信息：

```go
res, err := bosClient.GetBucketLogging(bucketName)
fmt.Println(res.Status)
fmt.Println(res.TargetBucket)
fmt.Println(res.TargetPrefix)
```

### 关闭Bucket日志

需要关闭Bucket的日志功能是，只需调用删除接口即可实现：

```go
err := bosClient.DeleteBucketLogging(bucketName)
```


# 错误处理

GO语言以error类型标识错误，BOS支持两种错误见下表：

错误类型        |  说明
----------------|-------------------
BceClientError  | 用户操作产生的错误
BceServiceError | BOS服务返回的错误

用户使用SDK调用BOS相关接口，除了返回所需的结果之外还会返回错误，用户可以获取相关错误进行处理。实例如下：

```
// bosClient 为已创建的BOS Client对象
bucketLocation, err := bosClient.PutBucket("test-bucket")
if err != nil {
    switch realErr := err.(type) {
    case *bce.BceClientError:
        fmt.Println("client occurs error:", realErr.Error())
    case *bce.BceServiceError:
        fmt.Println("service occurs error:", realErr.Error())
    default:
        fmt.Println("unknown error:", err)
    }
} else {
    fmt.Println("create bucket success, bucket location:", bucketLocation)
}
```

## 客户端异常

客户端异常表示客户端尝试向BOS发送请求以及数据传输时遇到的异常。例如，当发送请求时网络连接不可用时，则会返回BceClientError；当上传文件时发生IO异常时，也会抛出BceClientError。

## 服务端异常

当BOS服务端出现异常时，BOS服务端会返回给用户相应的错误信息，以便定位问题。常见服务端异常可参见[BOS错误信息格式](https://cloud.baidu.com/doc/BOS/API.html#.E9.94.99.E8.AF.AF.E4.BF.A1.E6.81.AF.E6.A0.BC.E5.BC.8F)

## SDK日志

BOS GO SDK自行实现了支持六个级别、三种输出（标准输出、标准错误、文件）、基本格式设置的日志模块，导入路径为`github.com/baidubce/bce-sdk-go/util/log`。输出为文件时支持设置五种日志滚动方式（不滚动、按天、按小时、按分钟、按大小），此时还需设置输出日志文件的目录。详见示例代码。

### 默认日志

BOS GO SDK自身使用包级别的全局日志对象，该对象默认情况下不记录日志，如果需要输出SDK相关日志需要用户自定指定输出方式和级别，详见如下示例：

```
// import "github.com/baidubce/bce-sdk-go/util/log"

// 指定输出到标准错误，输出INFO及以上级别
log.SetLogHandler(log.STDERR)
log.SetLogLevel(log.INFO)

// 指定输出到标准错误和文件，DEBUG及以上级别，以1GB文件大小进行滚动
log.SetLogHandler(log.STDERR | log.FILE)
log.SetLogDir("/tmp/gosdk-log")
log.SetRotateType(log.ROTATE_SIZE)
log.SetRotateSize(1 << 30)

// 输出到标准输出，仅输出级别和日志消息
log.SetLogHandler(log.STDOUT)
log.SetLogFormat([]string{log.FMT_LEVEL, log.FMT_MSG})
```

说明：
  1. 日志默认输出级别为`DEBUG`
  2. 如果设置为输出到文件，默认日志输出目录为`/tmp`，默认按小时滚动
  3. 如果设置为输出到文件且按大小滚动，默认滚动大小为1GB
  4. 默认的日志输出格式为：`FMT_LEVEL, FMT_LTIME, FMT_LOCATION, FMT_MSG`

### 项目使用

该日志模块无任何外部依赖，用户使用GO SDK开发项目，可以直接引用该日志模块自行在项目中使用，用户可以继续使用GO SDK使用的包级别的日志对象，也可创建新的日志对象，详见如下示例：

```
// 直接使用包级别全局日志对象（会和GO SDK自身日志一并输出）
log.SetLogHandler(log.STDERR)
log.Debugf("%s", "logging message using the log package in the BOS go sdk")

// 创建新的日志对象（依据自定义设置输出日志，与GO SDK日志输出分离）
myLogger := log.NewLogger()
myLogger.SetLogHandler(log.FILE)
myLogger.SetLogDir("/home/log")
myLogger.SetRotateType(log.ROTATE_SIZE)
myLogger.Info("this is my own logger from the BOS go sdk")
```


# 版本变更记录

## v0.9.2 [2018-3-16]

 - 修复go get下载时的错误提示信息
 - 修复重试请求时请求的body流丢失的问题
 - 完善UploadSuperFile返回的错误提示信息
 - 将GeneratePresignedUrl接口统一调整为bucket virtual hosting模式

## v0.9.1 [2018-1-4]

首次发布：

 - 创建、查看、罗列、删除Bucket，获取位置和判断是否存在
 - 支持管理Bucket的生命周期、日志、ACL、存储类型
 - 上传、下载、删除、罗列Object，支持分块上传、分块拷贝
 - 提供AppendObject功能和FetchObject功能
 - 封装并发的下载和分块上传接口
