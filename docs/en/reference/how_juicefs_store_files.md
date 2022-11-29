---
sidebar_label: How JuiceFS Stores Files
sidebar_position: 5
slug: /how_juicefs_store_files
---
# How JuiceFS Stores Files

The file system acts as a medium for interaction between user and hard drive, which allows files to be stored on the hard drive properly. As you know, the file systems FAT32 and NTFS are commonly used on Windows, while Ext4, XFS and Btrfs are commonly used on Linux. Each file system has its own unique way of organizing and managing files, which determines the file system features such as storage capacity and performance.

The strong consistency and high performance of JuiceFS are ascribed to its unique file management mode. Unlike the traditional file system that can only use local disks to store data and corresponding metadata, JuiceFS formats data first and store the data in object storage (cloud storage) with the corresponding metadata being stored in databases such as Redis.

Each file stored in JuiceFS is split into **"Chunk"**(s) (with a size limit of 64 MiB). Each Chunk is composed of one or more **"Slice"**(s), and the length of the slice varies depending on how the file is written. Each slice is composed of **"Block"**(s), which is 4 MiB by default. These blocks will eventually be stored in the object storage, while the metadata of the file and its Chunks, Slices, and Blocks will be stored in the metadata engine.

![](../images/juicefs-storage-format-new.png)

Using JuiceFS, files will eventually be split into Chunks and stored in object storage (not for small files though, small files will not be consolidated in JuiceFS, to avoid read amplification). Therefore, you may notice that the original files stored in JuiceFS cannot be found directly in the object storage, instead, you'll only see a directory of chunks and a bunch of numbered directories and files in the bucket. Don't panic! That's exactly what makes JuiceFS a high-performance file system.

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)
