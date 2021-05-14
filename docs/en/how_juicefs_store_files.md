# How JuiceFS stores files

The `file system` acts as a medium for interaction between the user and the hard drive, which allows files to be stored on the hard drive properly. As you know, Windows commonly used file systems are FAT32, NTFS, Linux commonly used file systems are Ext4, XFS, BTRFS, etc., each file system has its own unique way of organizing and managing files, which determines the file system Features such as storage capacity and performance.

As a file system, JuiceFS is no exception. Its strong consistency and high performance are inseparable from its unique file management mode.

Unlike the traditional file system that can only use local disks to store data and corresponding metadata, JuiceFS will format the data and store it in object storage (cloud storage), and store the metadata corresponding to the data in databases such as Redis. .

Any file stored in JuiceFS will be split into fixed-size **"Chunk"**, and the default upper limit is 64 MiB. Each Chunk is composed of one or more **"Slice"**. The length of the slice is not fixed, depending on the way the file is written. Each slice will be further split into fixed-size **"Block"**, which is 4 MiB by default. Finally, these blocks will be stored in the object storage. At the same time, JuiceFS will store each file and its Chunks, Slices, Blocks and other metadata information in metadata engines.

![		](../images/juicefs-storage-format-new.png)

Using JuiceFS, files will eventually be split into Chunks, Slices and Blocks and stored in object storage. Therefore, you will find that the source files stored in JuiceFS cannot be found in the file browser of the object storage platform. There is a chunks directory and a bunch of digitally numbered directories and files in the bucket. Don't panic, this is the secret of the high-performance operation of the JuiceFS file system!

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)

## Go further

Now, you can refer to [Quick Start Guide](quick_start_guide.md) to start using JuiceFS immediately!

You can also learn more about [JuiceFS Technical Architecture](architecture.md)

