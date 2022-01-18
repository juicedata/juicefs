# Data Encryption

## Data Encryption In Transit

JuiceFS encrypts data during transmission over the network to prevent unauthorized users from eavesdropping on network traffic.

JuiceFS clients always use HTTPS to upload data to the Object Storage Service, except for the following cases.

- Uploading to Alibaba Cloud OSS using internal endpoints
- Uploading to UCloud US3 using internal endpoints


## Data Encryption At Rest

JuiceFS supports Data Encryption At Rest. Any data will be encrypted first
before uploading to the object store. With such ability, JuiceFS can effectively prevent data leakage as along as the encryption key is safe and sound.

JuiceFS uses industry-standard encryption methods (AES-GCM and RSA) in client-side. Encryption and decryption are performed on the JuiceFS client side. 
The user only need to do one thing is providing a private key or password when JuiceFS is mounted and uses it like a normal file system. 
After the setup, the mounted file system is completely transparent to the application.

> **NOTE**: The cached data on the client-side is **NOT** encrypted. However, only the root user or owner can access this data. To encrypt the cached data as well, you can put the cached directory in an encrypted file system or block storage.


### Encryption and Decryption Method

A global RSA private key `M` must be created for each encrypted file system. Each object stored in the object store will have its own random symmetric key `S`. Data is encrypted with the symmetric key `S` for AES-GCM encryption, `S` is encrypted with the global RSA private key `M`, and the RSA private key is encrypted using a user-specified passphrase.

![Encryption At-rest](../images/encryption.png)

The detailed process of data encryption is as follows:
- Before writing to the object store, the data blocks are compressed using LZ4 or ZStandard.
- A random 256-bit symmetric key `S` and a random seed `N` are generated for each data block.
- AES-GCM-based encryption of each data block using `S` and `N` yields `encrypted_data`.
- To avoid the symmetric key `S` from being transmitted in clear text over the network, the symmetric key `S` is encrypted with the RSA key `M` to obtain the ciphertext `K`.
- The encrypted data `encrypted_data`, the ciphertext `K`, and the random seed `N` are combined into an object and then written to the object storage.

The steps for decrypting the data are as follows:
- Read the entire encrypted object (it may be a bit larger than 4MB).
- Parse the object data to get the ciphertext `K`, the random seed `N`, and the encrypted data `encrypted_data`.
- Decrypt `K` with RSA key to get symmetric key `S`.
- Decrypt the data `encrypted_data` based on AES-GCM using `S` and `N` to get the data block plaintext.
- Decompress the data block.


### Key Management

The security of RSA keys is critical when data at rest encryption is enabled. If the key is compromised, it may lead to data leakage. If the key is lost, then **all** encrypted data will be lost and cannot be recovered.

When creating a new volume using `juicefs format`, static encryption can be enabled by specifying the RSA private key with the `-encrypt-rsa-key` parameter, which will be saved to Redis. When the private key is password-protected, the password can be specified using the environment variable `JFS_RSA_PASSPHRASE`.

Usage:

1. Generate RSA key

```shell
$ openssl genrsa -out my-priv-key.pem -aes256 2048
```

2. Provide the key when formatting

```shell
$ juicefs format --encrypt-rsa-key my-priv-key.pem META-URL NAME
```

> **NOTE**: If the private key is password-protected, an environment variable `JFS_RSA_PASSPHRASE` should be exported first before executing `juicefs mount`.


### Performance

TLS, HTTPS, and AES-256 are implemented very efficiently in modern CPUs. Therefore, enabling encryption does not have a significant impact on file system performance. RSA algorithms are relatively slow, especially the decryption process. It is recommended to use 2048-bit RSA keys for storage encryption. Using 4096-bit keys may have a significant impact on reading performance.
