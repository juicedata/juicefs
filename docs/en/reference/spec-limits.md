# Specification Limits

## File System Limits

Below are theoretical limits for JuiceFS, numbers are very large so don't worry about them in practical use.

* Directory tree depth: unlimited
* File name length: 255 Bytes
* Symbolic link length: 4096 Bytes
* Number of hard links: 2^31
* Number of files in single directory: 2^31
* Number of files in a single volume: unlimited
* Single file size: 2^(26+31)
* Total file size: 4EiB
