---
sidebar_label: 社区集成
sidebar_position: 2
slug: /integrations
---

# 社区集成

## SDK

- [旷视科技](https://megvii.com) 团队贡献了 [Python SDK](https://github.com/megvii-research/juicefs-python)。

## AI

- [云知声](https://www.unisound.com) 团队参与开发 [Fluid](https://github.com/fluid-cloudnative/fluid) JuiceFSRuntime 缓存引擎，具体请参考[文档](https://github.com/fluid-cloudnative/fluid/blob/master/docs/zh/samples/juicefs_runtime.md) 。
- [PaddlePaddle](https://github.com/paddlepaddle/paddle) 团队已将 JuiceFS 缓存加速特性集成到 [Paddle Operator](https://github.com/PaddleFlow/paddle-operator) 中，具体请参考[文档](https://github.com/PaddleFlow/paddle-operator/blob/sampleset/docs/zh_CN/ext-overview.md)。
- 通过 JuiceFS 可以轻松搭建一个 [Milvus](https://milvus.io) 向量搜索引擎，Milvus 团队已经撰写了官方 [案例](https://zilliz.com/blog/building-a-milvus-cluster-based-on-juicefs) 与 [教程](https://tutorials.milvus.io/en-juicefs/index.html?index=..%2F..index#0)。

## 大数据

- 大数据 OLAP 分析引擎 [Apache Kylin 4.0](http://kylin.apache.org) 可以使用 JuiceFS 在所有公有云上轻松部署存储计算分离架构的集群，请看 [视频分享](https://www.bilibili.com/video/BV1c54y1W72S) 和 [案例文章](https://juicefs.com/zh-cn/blog/optimize-kylin-on-juicefs/)。
- [Apache Hudi](https://hudi.apache.org) 自 v0.10.0 版本开始支持 JuiceFS，你可以参考[官方文档](https://hudi.apache.org/docs/jfs_hoodie)了解如何配置 JuiceFS。
