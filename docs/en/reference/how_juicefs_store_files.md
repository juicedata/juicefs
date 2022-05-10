---
sidebar_label: How JuiceFS Stores Files
sidebar_position: 5
slug: /how_juicefs_store_files
---
# How JuiceFS Stores Files

The file system acts as a medium for interaction between user and hard drive, which allows files to be stored on the hard drive properly. As you know, the file systems FAT32 and NTFS are commonly used on Windows, while Ext4, XFS and Btrfs are commonly used on Linux. Each file system has its own unique way of organizing and managing files, which determines the file system features such as storage capacity and performance.

The strong consistency and high performance of JuiceFS are ascribed to its unique file management mode. Unlike the traditional file system that can only use local disks to store data and corresponding metadata, JuiceFS formats data first and store the data in object storage (cloud storage) with the corresponding metadata being stored in databases such as Redis.

Each file stored in JuiceFS is split into size-fixed **"Chunk"** s (64 MiB by default). Each Chunk is composed of one or more **"Slice"** (s), and the length of the slice varies depending on how the file is written. Each slice is composed of size-fixed **"Block"** s, which is 4 MiB by default. These blocks will be stored in object storage in the end; at the same time, the metadata information of the file and its Chunks, Slices, and Blocks will be stored in metadata engines via JuiceFS.

![](../images/juicefs-storage-format-new.png)

While using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you may notice that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform; instead, there are only a directory of chunks and a bunch of directories and files named by numbers in the bucket. Don't panic! That's exactly what makes JuiceFS a high-performance file system.

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)
