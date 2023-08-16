---
title: 监控与数据可视化
sidebar_position: 3
description: 本文介绍如何使用 Prometheus、Grafana 等第三方工具收集及可视化 JuiceFS 监控指标。
---

JuiceFS 客户端通过监控 API 对外暴露 [Prometheus](https://prometheus.io) 格式的实时监控指标，用户自行配置 Prometheus 抓取监控数据，然后通过 [Grafana](https://grafana.com) 等工具即可实现数据可视化。

## 在 Prometheus 中添加抓取配置 {#add-scrape-config}

在宿主机挂载 JuiceFS 后，默认可以通过 `http://localhost:9567/metrics` 地址获得客户端输出的实时指标数据。其他不同类型的 JuiceFS 客户端（CSI 驱动、S3 网关、Hadoop SDK）收集指标数据的方式略有区别，详见[「收集监控指标」](#collect-metrics)。

![Prometheus-client-data](../images/prometheus-client-data.jpg)

这里以收集挂载点的监控指标为例，在 [`prometheus.yml`](https://prometheus.io/docs/prometheus/latest/configuration/configuration) 中添加抓取配置（`scrape_configs`），指向 JuiceFS 客户端的监控 API 地址：

```yaml {20-22}
global:
  scrape_interval: 15s
  evaluation_interval: 15s

alerting:
  alertmanagers:
    - static_configs:
        - targets:
          # - alertmanager:9093

rule_files:
  # - "first_rules.yml"
  # - "second_rules.yml"

scrape_configs:
  - job_name: "prometheus"
    static_configs:
      - targets: ["localhost:9090"]

  - job_name: "juicefs"
    static_configs:
      - targets: ["localhost:9567"]
```

启动 Prometheus 服务：

```shell
./prometheus --config.file=prometheus.yml
```

访问 `http://localhost:9090` 即可看到 Prometheus 的界面。

## 通过 Grafana 进行数据可视化 {#grafana}

在 Grafana 中新建 Prometheus 类型的数据源：

- **Name**：为了便于识别，可以填写文件系统的名称。
- **URL**：Prometheus 的数据接口，默认为 `http://localhost:9090`。

![Grafana-data-source](../images/grafana-data-source.jpg)

JuiceFS 提供一些 Grafana 的仪表盘模板，将模板导入以后就可以展示收集上来的监控指标。目前提供的仪表盘模板有：

| 模板名称                                                                                                        | 说明                                                                         |
|-----------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------|
| [`grafana_template.json`](https://github.com/juicedata/juicefs/blob/main/docs/en/grafana_template.json)         | 用于展示自挂载点、S3 网关（非 Kubernetes 部署）及 Hadoop Java SDK 收集的指标 |
| [`grafana_template_k8s.json`](https://github.com/juicedata/juicefs/blob/main/docs/en/grafana_template_k8s.json) | 用于展示自 Kubernetes CSI 驱动、S3 网关（Kubernetes 部署）收集的指标         |

Grafana 仪表盘如下图：

![grafana_dashboard](../images/grafana_dashboard.png)

## 收集监控指标 {#collect-metrics}

根据部署 JuiceFS 方式的不同可以有不同的收集监控指标的方法，下面分别介绍。

### 宿主机挂载点 {#mount-point}

当通过 [`juicefs mount`](../reference/command_reference.md#mount) 命令挂载 JuiceFS 文件系统后，可以通过 `http://localhost:9567/metrics` 这个地址收集监控指标，你也可以通过 `--metrics` 选项自定义。如：

```shell
juicefs mount --metrics localhost:9567 ...
```

你可以使用命令行工具查看这些监控指标：

```shell
curl http://localhost:9567/metrics
```

除此之外，每个 JuiceFS 文件系统的根目录还有一个叫做 `.stats` 的隐藏文件，通过这个文件也可以查看监控指标。例如（这里假设挂载点的路径是 `/jfs`）：

```shell
cat /jfs/.stats
```

:::tip 提示
如果想要实时查看监控指标，可以使用 [`juicefs stats`](../administration/fault_diagnosis_and_analysis.md#stats) 命令。
:::

### Kubernetes {#kubernetes}

参考 [CSI 驱动文档](https://juicefs.com/docs/zh/csi/administration/going-production#monitoring)。

### S3 网关 {#s3-gateway}

:::note 注意
该特性需要运行 0.17.1 及以上版本 JuiceFS 客户端
:::

[JuiceFS S3 网关](../deployment/s3_gateway.md)默认会在 `http://localhost:9567/metrics` 这个地址提供监控指标，你也可以通过 `--metrics` 选项自定义。如：

```shell
juicefs gateway --metrics localhost:9567 ...
```

如果你是[在 Kubernetes 中部署](../deployment/s3_gateway.md#deploy-in-kubernetes) JuiceFS S3 网关，可以参考 [Kubernetes](#kubernetes) 小节的 Prometheus 配置来收集监控指标（区别主要在于 `__meta_kubernetes_pod_label_app_kubernetes_io_name` 这个标签的正则表达式），例如：

```yaml {6-8}
scrape_configs:
  - job_name: 'juicefs-s3-gateway'
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
        action: keep
        regex: juicefs-s3-gateway
      - source_labels: [__address__]
        action: replace
        regex: ([^:]+)(:\d+)?
        replacement: $1:9567
        target_label: __address__
      - source_labels: [__meta_kubernetes_pod_node_name]
        target_label: node
        action: replace
```

#### 通过 Prometheus Operator 收集 {#prometheus-operator}

[Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator) 让用户在 Kubernetes 环境中能够快速部署和管理 Prometheus，借助 Prometheus Operator 提供的 `ServiceMonitor` CRD 可以自动生成抓取配置。例如（假设 JuiceFS S3 网关的 `Service` 部署在 `kube-system` 名字空间）：

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: juicefs-s3-gateway
spec:
  namespaceSelector:
    matchNames:
      - kube-system
  selector:
    matchLabels:
      app.kubernetes.io/name: juicefs-s3-gateway
  endpoints:
    - port: metrics
```

有关 Prometheus Operator 的更多信息，请查看[官方文档](https://prometheus-operator.dev/docs/user-guides/getting-started)。

### Hadoop Java SDK {#hadoop}

[JuiceFS Hadoop Java SDK](../deployment/hadoop_java_sdk.md) 支持把监控指标上报到 [Pushgateway](https://github.com/prometheus/pushgateway) 或者 [Graphite](https://graphiteapp.org)。

#### Pushgateway

启用指标上报到 Pushgateway：

```xml
<property>
  <name>juicefs.push-gateway</name>
  <value>host:port</value>
</property>
```

同时可以通过 `juicefs.push-interval` 配置修改上报指标的频率，默认为 10 秒上报一次。

:::info 说明
根据 [Pushgateway 官方文档](https://github.com/prometheus/pushgateway/blob/master/README.md#configure-the-pushgateway-as-a-target-to-scrape)的建议，Prometheus 的[抓取配置](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#scrape_config)中需要设置 `honor_labels: true`。

需要特别注意，Prometheus 从 Pushgateway 抓取的指标的时间戳不是 JuiceFS Hadoop Java SDK 上报时的时间，而是抓取时的时间，具体请参考 [Pushgateway 官方文档](https://github.com/prometheus/pushgateway/blob/master/README.md#about-timestamps)。

默认情况下 Pushgateway 只会在内存中保存指标，如果需要持久化到磁盘上，可以通过 `--persistence.file` 选项指定保存的文件路径以及 `--persistence.interval` 选项指定保存到文件的频率（默认 5 分钟保存一次）。
:::

:::note 注意
每一个使用 JuiceFS Hadoop Java SDK 的进程会有唯一的指标，而 Pushgateway 会一直记住所有收集到的指标，导致指标数持续积累占用过多内存，也会使得 Prometheus 抓取指标时变慢，建议定期清理 Pushgateway 上的指标。

定期使用下面的命令清理 Pushgateway 的指标数据，清空指标不影响运行中的 JuiceFS Hadoop Java SDK 持续上报数据。**注意 Pushgateway 启动时必须指定 `--web.enable-admin-api` 选项，同时以下命令会清空 Pushgateway 中的所有监控指标。**

```bash
curl -X PUT http://host:9091/api/v1/admin/wipe
```

:::

有关 Pushgateway 的更多信息，请查看[官方文档](https://github.com/prometheus/pushgateway/blob/master/README.md)。

#### Graphite

启用指标上报到 Graphite：

```xml
<property>
  <name>juicefs.push-graphite</name>
  <value>host:port</value>
</property>
```

同时可以通过 `juicefs.push-interval` 配置修改上报指标的频率，默认为 10 秒上报一次。

JuiceFS Hadoop Java SDK 支持的所有配置参数请参考[文档](../deployment/hadoop_java_sdk.md#客户端配置参数)。

### 使用 Consul 作为注册中心 {#use-consul}

:::note 注意
该特性需要运行 1.0.0 及以上版本 JuiceFS 客户端
:::

JuiceFS 支持使用 Consul 作为监控指标 API 的注册中心，默认的 Consul 地址是 `127.0.0.1:8500`，你也可以通过 `--consul` 选项自定义。如：

```shell
juicefs mount --consul 1.2.3.4:8500 ...
```

当配置了 Consul 地址以后，`--metrics` 选项不再需要配置，JuiceFS 将会根据自身网络与端口情况自动配置监控指标 URL。如果同时设置了 `--metrics`，则会优先尝试监听配置的 URL。

注册到 Consul 上的每个服务，其[服务名](https://developer.hashicorp.com/consul/docs/services/configuration/services-configuration-reference#name)都为 `juicefs`，[服务 ID](https://developer.hashicorp.com/consul/docs/services/configuration/services-configuration-reference#id) 的格式为 `<IP>:<mount-point>`，例如：`127.0.0.1:/tmp/jfs`。

每个服务的 [`meta`](https://developer.hashicorp.com/consul/docs/services/configuration/services-configuration-reference#meta) 都包含 `hostname` 与 `mountpoint` 两个 key，对应的值分别表示挂载点所在的主机名和挂载点路径。特别地，S3 网关的 `mountpoint` 值总是为 `s3gateway`。

成功注册到 Consul 上以后，需要在 `prometheus.yml` 中新增 [`consul_sd_config`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#consul_sd_config) 配置，在 `services` 中填写 `juicefs`。

## 监控指标索引 {#metrics-reference}

参考[「JuiceFS 监控指标」](../reference/p8s_metrics.md)。
