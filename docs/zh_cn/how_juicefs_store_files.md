# JuiceFS 如何存储文件

`文件系统` 作为用户和硬盘之间交互的媒介，它让文件可以妥善的被存储在硬盘上。如你所知，Windows  常用的文件系统有 FAT32、NTFS，Linux 常用的文件系统有 Ext4、XFS、BTRFS 等，每一种文件系统都有其独特的组织和管理文件的方式，它决定了文件系统的存储能力和性能等特征。

JuiceFS 作为一个文件系统也不例外，它的强一致性、高性能等特征离不开它独特的文件管理模式。

与传统文件系统只能使用本地磁盘存储数据和对应的元数据的模式不同，JuiceFS 会将数据格式化以后存储在对象存储（云存储），同时会将数据对应的元数据存储在 Redis 等数据库中。

任何存入 JuiceFS 的文件都会被拆分成固定大小的 **"Chunk"**，默认的容量上限是 64 MiB。每个 Chunk 由一个或多个 **"Slice"** 组成，Slice 的长度不固定，取决于文件写入的方式。每个 Slice 又会被进一步拆分成固定大小的 **"Block"**，默认为 4 MiB。最后，这些 Block 会被存储到对象存储。与此同时，JuiceFS 会将每个文件以及它的 Chunks、Slices、Blocks 等元数据信息存储在元数据引擎中。

![JuiceFS storage format](../images/juicefs-storage-format-new.png)

使用 JuiceFS，文件最终会被拆分成 Chunks、Slices 和 Blocks 存储在对象存储。因此，你会发现在对象存储平台的文件浏览器中找不到存入 JuiceFS 的源文件，存储桶中只有一个 chunks 目录和一堆数字编号的目录和文件。不要惊慌，这正是 JuiceFS 文件系统高性能运作的秘诀！

![How JuiceFS stores your files](../images/how-juicefs-stores-files-new.png)

## 你可能还需要

现在，你可以参照 [快速上手指南](quick_start_guide.md) 立即开始使用 JuiceFS！

你还可以进一步了解 [JuiceFS 的技术架构](architecture.md)

