---
sidebar_position: 7
---

# Specification Limits

## File System Limits

Below are theoretical limits for JuiceFS, in real use, performance and file system size will be limited by the metadata engine and object storage of your choice.

* Directory tree depth: unlimited
* File name length: 255 Bytes
* Symbolic link length: 4096 Bytes
* Number of hard links: 2^31
* Number of files in single directory: 2^31
* Number of files in a single volume: unlimited
* Single file size: 2^(26+31)
* Total file size: 4EiB
