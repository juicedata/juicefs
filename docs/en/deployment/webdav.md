---
title: Deploy WebDAV Server
sidebar_position: 5
---

WebDAV is an extension of the HTTP protocol, a sharing protocol that facilitates collaborative editing and management of documents on a network between multiple users. WebDAV client support is built into many tools involved in file editing and synchronization, macOS Finder, and the file managers of some Linux distributions.

JuiceFS supports accessing through the WebDAV protocol, which is very convenient for macOS and other operating systems that do not have native FUSE support.

## Pre-requisites

Before you can configure a WebDAV server, you need to [create a JuiceFS file system](../getting-started/standalone.md#juicefs-format).

## Anonymous WebDAV

For security insensitive environments such as standalone or intranet, anonymous WebDAV without authentication can be configured with the following command format.

```shell
juicefs webdav META-URL LISTENING-ADDRESS:PORT
```

For example, enable the WebDAV access protocol for a JuiceFS file system:

```shell
sudo juicefs webdav sqlite3://myjfs.db 192.168.1.8:80
```

WebDAV server needs to be accessed through the set listening address and port, such as the above example uses the IP address `192.168.1.8` of the intranet, and the standard Web port number `80`, when accessing without specifying the port, directly access `http://192.168.1.8`.

If you use another port number, you need to specify it explicitly in the address, for example, if you listen to port `9007`, the access address should be `http://192.168.1.8:9007`.

:::tip
Do not use "Guest" identity when accessing anonymous WebDAV using macOS's Finder. Please use "Registered User" identity, user name can enter any character, password can be empty, and then connect directly.
:::

## WebDAV with authentication

:::info
JuiceFS v1.0.3 and previous versions do not support authentication features.
:::

The WebDAV authentication feature of JuiceFS requires setting the user name (`WEBDAV_USER`) and password (`WEBDAV_PASSWORD`) through environment variables, e.g.:

```shell
export WEBDAV_USER=user
export WEBDAV_PASSWORD=mypassword
sudo juicefs webdav sqlite3://myjfs.db 192.168.1.8:80
```

## Enable HTTPS support

JuiceFS supports configuring WebDAV server protected by the HTTPS protocol, specifying certificates and private keys through `--cert-file` and `--key-file` options, either using a certificate issued by a trusted digital certificate authority CA or using OpenSSL to create self-signed certificate.

### Self-signed certificate

To create a private key and certificate using OpenSSL.

1. Generate server private key

   ```shell
   openssl genrsa -out client.key 4096
   ```

2. Generate Certificate Signing Request (CSR)

   ```shell
   openssl req -new -key client.key -out client.csr
   ```

3. Issuing certificates using CSR

   ```shell
   openssl x509 -req -days 365 -in client.csr -signkey client.key -out client.crt
   ```

The above command will produce the following files in the current directory:

- `client.key`: Server private Key
- `client.csr`: Certificate Signing Request file
- `client.crt`: Self-signed certificate

To create a WebDAV server you need to use `client.key` and `client.crt`, e.g.

```shell
sudo juicefs webdav \
   --cert-file ./client.crt \
   --key-file ./client.key \
   sqlite3://myjfs.db 192.168.1.8:443
```

With HTTPS support enabled, the listening port number can be changed to the standard HTTPS port number `443`, and then the `https://` protocol is used instead, so that the port number does not need to be specified when accessing, for example: `https://192.168.1.8`.

Likewise, if a non-HTTPS standard port number is set, it should be explicitly specified in the access address, e.g., if you set a port to listen on `9999`, the access address should be `https://192.168.1.8:9999`.
