---
sidebar_label: Monitoring
sidebar_position: 6
---

# Monitoring

JuiceFS provides a [Prometheus](https://prometheus.io) API for each file system (the default API address is `http://localhost:9567/metrics`), which can be used to collect JuiceFS monitoring metrics. Once the monitoring metrics are collected, they can be quickly displayed via the [Grafana](https://grafana.com) dashboard template provided by JuiceFS.

## Collecting monitoring metrics

There are different ways to collect monitoring metrics depending on how JuiceFS is deployed, which are described below.

### Mount point

When the JuiceFS file system is mounted via the [`juicefs mount`](../reference/command_reference.md#juicefs-mount) command, you can collect monitoring metrics via the address `http://localhost:9567/metrics`, or you can customize it via the `--metrics` option. For example:

```shell
$ juicefs mount --metrics localhost:9568 ...
```

You can view these monitoring metrics using the command line tool:

```shell
$ curl http://localhost:9567/metrics
```

In addition, the root directory of each JuiceFS file system has a hidden file called `.stats`, through which you can also view monitoring metrics. For example (assuming here that the path to the mount point is `/jfs`):

```shell
$ cat /jfs/.stats
```

### Kubernetes

The [JuiceFS CSI Driver](../deployment/how_to_use_on_kubernetes.md) will provide monitoring metrics on the `9567` port of the mount pod by default, or you can customize it by adding the `metrics` option to the `mountOptions` (please refer to the [CSI Driver documentation](https://juicefs.com/docs/csi/examples/mount-options) for how to modify `mountOptions`), e.g.:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: juicefs-pv
  labels:
    juicefs-name: ten-pb-fs
spec:
  ...
  mountOptions:
    - metrics=0.0.0.0:9568
```

Add a crawl job to `prometheus.yml` to collect monitoring metrics:

```yaml
scrape_configs:
  - job_name: 'juicefs'
    kubernetes_sd_configs:
    - role: pod
    relabel_configs:
    - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
      action: keep
      regex: juicefs-mount
    - source_labels: [__address__]
      action: replace
      regex: ([^:]+)(:\d+)?
      replacement: $1:9567
      target_label: __address__
    - source_labels: [__meta_kubernetes_pod_node_name]
      target_label: node
      action: replace
```

Here assume the Prometheus server is running inside Kubernetes cluster, if your Prometheus server is running outside Kubernetes cluster, make sure Kubernetes cluster nodes are reachable from Prometheus server, refer to [this issue](https://github.com/prometheus/prometheus/issues/4633) to add the `api_server` and `tls_config` client auth to the above configuration like this:

```yaml
scrape_configs:
  - job_name: 'juicefs'
    kubernetes_sd_configs:
    - api_server: <Kubernetes API Server>
      role: pod
      tls_config:
        ca_file: <...>
        cert_file: <...>
        key_file: <...>
        insecure_skip_verify: false
    relabel_configs:
    ...
```

### S3 Gateway

:::note
This feature needs to run JuiceFS client version 0.17.1 and above.
:::

The [JuiceFS S3 Gateway](../deployment/s3_gateway.md) will provide monitoring metrics at the address `http://localhost:9567/metrics` by default, or you can customize it with the `-metrics` option. For example:

```shell
$ juicefs gateway --metrics localhost:9568 ...
```

If you are deploying JuiceFS S3 Gateway in Kubernetes, you can refer to the Prometheus configuration in the [Kubernetes](#kubernetes) section to collect monitoring metrics (the difference is mainly in the regular expression for the label `__meta_kubernetes_pod_label_app_kubernetes_io_name`), e.g.:

```yaml
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

#### Collected via Prometheus Operator

[Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator) enables users to quickly deploy and manage Prometheus in Kubernetes, with the help of the `ServiceMonitor` CRD provided by Prometheus Operator can automatically generate scrape configuration. For example (assuming that the `Service` of the JuiceFS S3 Gateway is deployed in the `kube-system` namespace):

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

For more information about Prometheus Operator, please check [official document](https://github.com/prometheus-operator/prometheus-operator/blob/main/Documentation/user-guides/getting-started.md).

### Hadoop

The [JuiceFS Hadoop Java SDK](../deployment/hadoop_java_sdk.md) supports reporting monitoring metrics to [Pushgateway](https://github.com/prometheus/pushgateway) and then letting Prometheus scrape the metrics from Pushgateway.

Please enable metrics reporting with the following configuration:

```xml
<property>
  <name>juicefs.push-gateway</name>
  <value>host:port</value>
</property>
```

At the same time, the frequency of reporting metrics can be modified through the `juicefs.push-interval` configuration. The default is to report once every 10 seconds. For all configurations supported by JuiceFS Hadoop Java SDK, please refer to [documentation](../deployment/hadoop_java_sdk.md#client-configurations).

:::info
According to the suggestion of [Pushgateway official document](https://github.com/prometheus/pushgateway/blob/master/README.md#configure-the-pushgateway-as-a-target-to-scrape), Prometheus's [scrape configuration](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#scrape_config) needs to set `honor_labels: true`.

It is important to note that the timestamp of the metrics scraped by Prometheus from Pushgateway is not the time when the JuiceFS Hadoop Java SDK reported it, but the time when it was scraped. For details, please refer to [Pushgateway official document](https://github.com/prometheus/pushgateway/blob/master/README.md#about-timestamps).

By default, Pushgateway will only save metrics in memory. If you need to persist to disk, you can specify the file path for saving with the `--persistence.file` option and the frequency of saving to the file with the `--persistence.interval` option (the default save time is 5 minutes).
:::

:::note
Each process using JuiceFS Hadoop Java SDK will have a unique metric, and Pushgateway will always remember all the collected metrics, resulting in the continuous accumulation of metrics and taking up too much memory, which will also slow down Prometheus scrapes metrics. It is recommended to clean up metrics on Pushgateway regularly.

Regularly use the following command to clean up the metrics of Pushgateway. Clearing the metrics will not affect the running JuiceFS Hadoop Java SDK to continuously report data. **Note that the `--web.enable-admin-api` option must be specified when Pushgateway is started, and the following command will clear all monitoring metrics in Pushgateway.**

```bash
$ curl -X PUT http://host:9091/api/v1/admin/wipe
```
:::

For more information about Pushgateway, please check [official document](https://github.com/prometheus/pushgateway/blob/master/README.md).

### Use Consul as registration center

:::note
This feature needs to run JuiceFS client version 1.0.0 and above.
:::

JuiceFS support use Consul as registration center for metrics API. The default Consul address is `127.0.0.1:8500`. You could custom the address through `--consul` option, e.g.:

```shell
$ juicefs mount --consul 1.2.3.4:8500 ...
```

When the Consul address is configured, the `--metrics` option does not need to be configured. JuiceFS will automatically configure metrics URL according to its own network and port conditions. If `--metrics` is set at the same time, it will first try to listen on the configured metrics URL.

For each instance registered to Consul, its `serviceName` is `juicefs`, and the format of `serviceId` is `<IP>:<mount-point>`, for example: `127.0.0.1:/tmp/jfs`.

The meta of each instance contains two aspects: `hostname` and `mountpoint`. When `mountpoint` is `s3gateway`, which means that the instance is an S3 gateway.

## Display monitoring metrics

### Grafana dashboard template

JuiceFS provides some dashboard templates for Grafana, which can be imported to show the collected metrics in Prometheus. The dashboard templates currently available are:

| Name                                                                                                            | Description                                                                                             |
| ----                                                                                                            | -----------                                                                                             |
| [`grafana_template.json`](https://github.com/juicedata/juicefs/blob/main/docs/en/grafana_template.json)         | For show metrics collected from mount point, S3 gateway (non-Kubernetes deployment) and Hadoop Java SDK |
| [`grafana_template_k8s.json`](https://github.com/juicedata/juicefs/blob/main/docs/en/grafana_template_k8s.json) | For show metrics collected from Kubernetes CSI Driver and S3 gateway (Kubernetes deployment)            |

A sample Grafana dashboard looks like this:

![JuiceFS Grafana dashboard](../images/grafana_dashboard.png)

## Monitoring metrics reference

Please refer to the ["JuiceFS Metrics"](../reference/p8s_metrics.md) document.
