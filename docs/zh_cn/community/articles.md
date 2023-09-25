---
title: JuiceFS 文章合集
sidebar_position: 2
slug: /articles
---

JuiceFS 广泛适用于各种数据存储和共享场景，本页汇总来自世界各地用户使用 JuiceFS 的实践和相关技术文章，欢迎大家共同维护这个列表。

## AI

- [构建易于运维的 AI 训练平台：存储选型与最佳实践](https://juicefs.com/zh-cn/blog/user-stories/easy-operate-ai-training-platform-storage-selection)，2023-08-04，孙冀川@思谋科技
- [之江实验室：如何基于 JuiceFS 为超异构算力集群构建存储层](https://juicefs.com/zh-cn/blog/user-stories/high-performance-scale-out-heterogeneous-computing-power-cluster-storage)，2023-06-09，洪晨@之江实验室
- [加速 AI 训练，如何在云上实现灵活的弹性吞吐](https://juicefs.com/zh-cn/blog/solutions/accelerate-ai-training-flexible-elastic-throughput-cloud)，2023-05-06，苏锐
- [如何借助分布式存储 JuiceFS 加速 AI 模型训练](https://juicefs.com/zh-cn/blog/usage-tips/how-to-use-juicefs-to-speed-up-ai-model-training)，2023-04-25，高昌健
- [vivo AI 计算平台的轩辕文件存储实践](https://www.infoq.cn/article/3oFSOWfYGsX5h7xzsIe6)，2022-10-18，彭毅格@vivo AI 计算平台团队
- [深势科技分享 AI 企业多云存储架构实践](https://juicefs.com/zh-cn/blog/user-stories/dptech-ai-storage-in-multi-cloud-practice)，2022-07-06，李样兵@深势科技
- [AI 场景存储优化：云知声超算平台基于 JuiceFS 的存储实践](https://juicefs.com/zh-cn/blog/user-stories/juicefs-support-ai-storage-at-unisound)，2022-06-28，吕冬冬@云知声
- [上汽云 x JuiceFS：iGear 用了这个小魔法，模型训练速度提升 300%](https://juicefs.com/zh-cn/blog/user-stories/performance-boost-3x-on-igear-platform)，2022-01-27，上汽云 iGear
- [PaddlePaddle x JuiceFS : 全新缓存组件，大幅加速云上飞桨分布式训练作业](https://juicefs.com/zh-cn/blog/solutions/juicefs-helps-paddlepaddle-boosting-performance)，2022-01-06，百度 PaddlePaddle 团队
- [如何在 Kubernetes 集群中玩转 Fluid + JuiceFS](https://juicefs.com/zh-cn/blog/solutions/fluid-with-juicefs)，2021-12-01，吕冬冬@云知声 & 朱唯唯@Juicedata
- [百亿级小文件存储，JuiceFS 在自动驾驶行业的最佳实践](https://juicefs.com/zh-cn/blog/user-stories/ten-billion-level-small-files-storage-juicefs-best-practice-in-the-autonomous-driving-industry)，2021-10-28，高昌健
- [初探云原生下的 AI 分布式文件系统-JuiceFS](https://mp.weixin.qq.com/s/AiI0lVgFwycmK9Rl-6KW4w)，2021-08-18，屈骏@梯度科技
- [如何借助 JuiceFS 为 AI 模型训练提速 7 倍](https://juicefs.com/blog/cn/posts/how-to-use-juicefs-to-speed-up-ai-model-training-by-7-times)

## 大数据

- [多点 DMALL：大数据存算分离下的存储架构探索与实践](https://juicefs.com/zh-cn/blog/user-stories/separation-of-storage--computing-building-cloud-native-big-data-platform), 2023-08-16，李铭@多点
- [网易互娱出海之旅：大数据平台上云架构设计与实践](https://juicefs.com/zh-cn/blog/user-stories/hadoop-compatible-storage-big-data-cloud-platform-s3)，2023-08-09，柯维鸿@网易互娱
- [Protobuf 在知乎大数据场景的应用，利用 JuiceFS 动态注入 JAR 包](https://zhuanlan.zhihu.com/p/586120009)，2022-11-23，胡梦宇@知乎
- [金山云：基于 JuiceFS 的 Elasticsearch 温冷热数据管理实践](https://juicefs.com/zh-cn/blog/user-stories/juicefs-elasticsearch-cold-heat-data-management)，2022-11-17，侯学峰@金山云
- [JuiceFS 替代 HDFS，苦 HDFS 小文件久矣](https://zhuanlan.zhihu.com/p/569586606)，2022-10-08，久耶供应链 大数据总监
- [JuiceFS 在 Elasticsearch/ClickHouse 温冷数据存储中的实践](https://juicefs.com/zh-cn/blog/solutions/juicefs-elasticsearch-clickhouse-hot-cold-data-storage)，2022-09-30，高昌健
- [从 Hadoop 到云原生，大数据平台如何做存算分离](https://juicefs.com/zh-cn/blog/solutions/hadoop-to-cloud-native-separation-of-compute-and-storage-for-big-data-platform)，2022-09-14，苏锐
- [理想汽车：从 Hadoop 到云原生的演进与思考](https://juicefs.com/zh-cn/blog/user-stories/li-auto-case-hadoop-cloud-native)，2022-08-30，聂磊@理想汽车
- [一面数据：Hadoop 迁移云上架构设计与实践](https://juicefs.com/zh-cn/blog/user-stories/yimiancase)，2022-07-28，刘畅&李阳良@一面数据
- [移动云使用 JuiceFS 支持 Apache HBase 增效降本的探索](https://juicefs.com/zh-cn/blog/user-stories/juicefs-support-hbase-at-chinamobile-cloud)，2022-05-31，陈海峰@移动云
- [JuiceFS 在数据湖存储架构上的探索](https://juicefs.com/zh-cn/blog/solutions/juicefs-exploration-on-data-lake-storage-architecture)，2022-04-28，高昌健
- [JuiceFS 在理想汽车的使用和展望](https://juicefs.com/zh-cn/blog/user-stories/li-auto-with-juicefs)，2022-01-21，聂磊@理想汽车
- [JuiceFS + HDFS 权限问题定位](https://mp.weixin.qq.com/s/9mIMPuljL-UxP9t7-3dKxw)，2021-12-31，李阳良@一面数据
- [知乎 x JuiceFS：利用 JuiceFS 给 Flink 容器启动加速](https://juicefs.com/zh-cn/blog/user-stories/zhihu-flink-with-juicefs)，2021-11-22，胡梦宇@知乎
- [Elasticsearch 存储成本省 60%，稿定科技干货分享](https://juicefs.com/zh-cn/blog/user-stories/gaoding-with-juicefs)，2021-10-09，稿定 SRE 团队
- [Shopee x JuiceFS：ClickHouse 冷热数据分离存储架构与实践](https://juicefs.com/zh-cn/blog/user-stories/shopee-clickhouse-with-juicefs)，2021-10-09，Teng@Shopee
- [JuiceFS on AWS EMR](https://www.youtube.com/watch?v=PFNOcqiW4-M&t=3s), Youtube video, Pahud Dev
- [JuiceFS 加速 Spark Shuffle](https://mp.weixin.qq.com/s/JGa2eYqM8db_OMU7SzZw8A)，2021-03-09，RespectM
- [JuiceFS 如何帮助趣头条超大规模 HDFS 降负载](https://juicefs.com/blog/cn/posts/qutoutiao-big-data-platform-user-case)
- [环球易购数据平台如何做到既提速又省钱？](https://juicefs.com/blog/cn/posts/globalegrow-big-data-platform-user-case)
- [JuiceFS 在大搜车数据平台的实践](https://juicefs.com/blog/cn/posts/juicefs-practice-in-souche)
- [使用 AWS Cloudformation 在 Amazon EMR 中一分钟配置 JuiceFS](https://aws.amazon.com/cn/blogs/china/use-aws-cloudformation-to-configure-juicefs-in-amazon-emr-in-one-minute)
- [使用 JuiceFS 在云上优化 Kylin 4.0 的存储性能](https://juicefs.com/blog/cn/posts/optimize-kylin-on-juicefs)
- [ClickHouse 存算分离架构探索](https://juicefs.com/blog/cn/posts/clickhouse-disaggregated-storage-and-compute-practice)
- [存算分离实践：JuiceFS 在中国电信日均 PB 级数据场景的应用](https://juicefs.com/zh-cn/blog/user-stories/applicatio-of-juicefs-in-china-telecoms-daily-average-pb-data-scenario)

## 云原生 & Kubernetes

- [小米云原生文件存储平台化实践：支撑 AI 训练、大模型、容器平台多项业务](https://juicefs.com/zh-cn/blog/user-stories/cloud-native-file-storage-platform-as-ai-training-large-models-container-platforms)，2023-09-22，孙佳朋@小米
- [从本地到云端：豆瓣如何使用 JuiceFS 实现统一的数据存储](https://juicefs.com/zh-cn/blog/user-stories/scalable-computing-unified-data-storage-ops-cloud-spark-k8s-juicefs)，2023-05-10，曹丰宇@豆瓣
- [云上大数据存储：探究 JuiceFS 与 HDFS 的异同](https://juicefs.com/zh-cn/blog/engineering/similarities-and-differences-between-hdfs-and-juicefs-structures)，2023-04-04，汤友棚
- [Sidecar-详解 JuiceFS CSI Driver 新模式](https://juicefs.com/zh-cn/blog/usage-tips/explain-in-detail-juicefs-csi-driver-sidecar)，2023-02-22，朱唯唯
- [存储更弹性，详解 Fluid“ECI 环境数据访问”新功能](https://juicefs.com/zh-cn/blog/solutions/fluid-eci-juicefs)，2022-09-05，朱唯唯
- [基于 JuiceFS 的 KubeSphere DevOps 项目数据迁移方案](https://mp.weixin.qq.com/s/RgUHRUrL0u-J9nVqwOfS8Q)，2022-08-04，尹珉@数跑科技
- [JuiceFS CSI Driver 架构设计详解](https://juicefs.com/zh-cn/blog/engineering/juicefs-csi-driver-arch-design)，2022-03-23，朱唯唯
- [JuiceFS 在火山引擎边缘计算的应用实践](https://juicefs.com/zh-cn/blog/user-stories/how-juicefs-accelerates-edge-rendering-performance-in-volcengine)，2023-02-17
，何兰州
- [使用 KubeSphere 应用商店 5 分钟内快速部署 JuiceFS](https://juicefs.com/zh-cn/blog/solutions/kubesphere-with-juicefs)，2021-11-19，尹珉@杭州数跑科技 & 朱唯唯@Juicedata
- [JuiceFS CSI Driver 的最佳实践](https://juicefs.com/zh-cn/blog/engineering/csi-driver-best-practices)，2021-11-08，朱唯唯
- [JuiceFS CSI Driver v0.10 全新架构解读](https://juicefs.com/zh-cn/blog/engineering/juicefs-csi-driver-v010)，2021-07-28，朱唯唯

## 数据共享

- [基于 JuiceFS 搭建 Milvus 分布式集群](https://juicefs.com/blog/cn/posts/build-milvus-distributed-cluster-based-on-juicefs)
- [如何解决 NAS 单点故障还顺便省了 90% 的成本？](https://juicefs.com/blog/cn/posts/modao-replace-nas-with-juicefs)

## 数据备份、迁移与恢复

- [突破存储数据量限制，JuiceFS 在携程海量冷数据场景下的实践](https://juicefs.com/zh-cn/blog/user-stories/xiecheng-case)，2022-08-29，妙成 & 小峰
- [40+ 倍提升，详解 JuiceFS 元数据备份恢复性能优化方法](https://juicefs.com/zh-cn/blog/engineering/juicefs-load-and-dump-optimization)，2022-07-13，执剑
- [利用 JuiceFS 把 MySQL 备份验证性能提升 10 倍](https://juicefs.com/blog/cn/posts/optimize-xtrabackup-prepare-by-oplog)
- [跨云数据搬迁利器：Juicesync](https://juicefs.com/blog/cn/posts/juicesync)
- [下厨房基于 JuiceFS 的 MySQL 备份实践](https://juicefs.com/blog/cn/posts/xiachufang-mysql-backup-practice-on-juicefs)
- [如何用 JuiceFS 归档备份 NGINX 日志](https://juicefs.com/blog/cn/posts/backup-nginx-logs-on-juicefs)

## 教程、使用指南、评测及其他

- [浅析 GlusterFS 与 JuiceFS 的架构异同](https://juicefs.com/zh-cn/blog/engineering/similarities-and-differences-between-glusterfs-and-juicefs-structures)，2023-08-23，Sandy
- [如何基于 JuiceFS 配置 Samba 和 NFS 共享？](https://juicefs.com/zh-cn/blog/usage-tips/configure-samba-and-nfs-shares-based-juicefs)，2023-08-04，于鸿儒
- [云上使用 Stable Diffusion，模型数据如何共享和存储？](https://juicefs.com/zh-cn/blog/usage-tips/share-store-model-data-stable-diffusion-cloud)，2023-06-16，于鸿儒
- [从架构到特性：JuiceFS 企业版首次全面解析](https://juicefs.com/zh-cn/blog/solutions/juicefs-enterprise-edition-features-vs-community-edition)，2023-06-06，高昌健
- [浅析三款大规模分布式文件系统架构设计](https://juicefs.com/zh-cn/blog/engineering/large-scale-distributed-filesystem-comparison)，2023-03-08，高昌健
- [浅析 SeaweedFS 与 JuiceFS 架构异同](https://juicefs.com/zh-cn/blog/engineering/similarities-and-differences-between-seaweedfs-and-juicefs-structures)，2023-02-10，陈杰
- [分布式文件系统 JuiceFS 测试总结](https://mp.weixin.qq.com/s/XFWQASQFt5FISip-mrYG4Q)，2022-09-13，邹秋波
- [JuiceFS 元数据引擎选型指南](https://juicefs.com/zh-cn/blog/usage-tips/juicefs-metadata-engine-selection-guide)，2022-10-12，Sandy
- [GitHub Codespaces 上分离计算和存储？ #JuiceFS 花式玩法#](https://mp.weixin.qq.com/s/geoYkruj6lkXOns7bib-qA)，2022-08-19，张俊帆
- [浅析 Redis 作为 JuiceFS 元数据引擎的优劣势](https://juicefs.com/zh-cn/blog/usage-tips/introduce-redis-as-juicefs-metadata-engine)，2022-07-22，高昌健
- [如何使用 etcd 实现分布式 /etc 目录](https://juicefs.com/zh-cn/blog/usage-tips/make-distributed-etc-directory-with-etcd-and-juicefs)，2022-06-23，朱唯唯
- [社区投稿｜小团队如何妙用 JuiceFS](https://mp.weixin.qq.com/s/AAw1I6f36h1pZjLELtQCow)，2022-04-01，timfeirg
- [在 Windows 上如何后台运行 JuiceFS](https://mp.weixin.qq.com/s/nMqCuit4zRoNCK4m-b0hxA)，2022-03-10，秦牧羊
- [JuiceFS 导出/导入元数据的优化之路](https://www.youtube.com/watch?v=MDMitDtLly4), Youtube Video
- [初探 JuiceFS](https://mp.weixin.qq.com/s/jTBAcmUiBMBvTutdOUHpcA)，2021-11-28，ahnselina
- [JuiceFS 源码阅读 - 上](https://mp.weixin.qq.com/s/mdqFJLpaJ249rUUEnRiP3Q)，2021-06-24，秦牧羊
- [JuiceFS 你应该知道的一些事](https://mp.weixin.qq.com/s/6ylBmUXy_3aQggznl65nHg)，2021-01-15，祝威廉@Kyligence

## 内容收录

如果你也想把自己的 JuiceFS 应用方案添加到这份案例列表中，可以采用以下几种投稿方式：

### GitHub 投稿

你可以通过 GitHub 创建本仓库的分支，将你的案例网页链接添加到相应的分类中，提交 Pull Request 申请，等待审核和分支合并。

### 社交媒体投稿

你可以加入 JuiceFS 官方的 [Slack 频道](https://go.juicefs.com/slack)，任何一位工作人员都可以接洽案例投稿事宜。
