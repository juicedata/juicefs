# Community CSI

## Start from the current public documentation

- [Introduction](https://juicefs.com/docs/csi/introduction)
- [Getting started](https://juicefs.com/docs/csi/getting_started)
- [Use JuiceFS volumes](https://juicefs.com/docs/csi/guide/pv)
- [Configurations](https://juicefs.com/docs/csi/guide/configurations)
- [Production recommendations](https://juicefs.com/docs/csi/administration/going-production)
- [Troubleshooting](https://juicefs.com/docs/csi/troubleshooting)
- [Upgrade the CSI Driver](https://juicefs.com/docs/csi/upgrade-csi-driver)

Match documentation and source investigation to the installed CSI Driver, JuiceFS client image, Helm chart or manifests, and Kubernetes version.

If the public site is unavailable, ask for a local CSI Driver documentation checkout and its release tag, commit, or download date. For the current source layout, use `<CSI_ROOT>/docs/en/` for English or `<CSI_ROOT>/docs/zh_cn/` for Chinese. Verify the layout and revision instead of assuming that a website path matches a source-tree path.

## Record the deployment shape

Identify:

- Kubernetes version and distribution;
- CSI Driver version and installation method;
- CSI Controller and CSI Node namespaces and images;
- JuiceFS client image and mount mode;
- dynamic or static provisioning;
- Secret, StorageClass, PV, PVC, workload, node, and Mount Pod involved;
- use of sidecar mode or JuiceFS Operator, if any.

For Community Edition, use only version-matched Community CSI fields. Do not add Cloud Service tokens, Enterprise Edition console fields, licenses, or managed-service assumptions.

Never expose Kubernetes Secret values. Request resource names and redacted field presence instead.

## Diagnose by stage

Keep the control and data paths separate:

1. **Provisioning:** the CSI Controller processes PVC or PV allocation and creates or updates Kubernetes resources.
2. **Scheduling:** kubelet and the CSI Node component prepare the selected node.
3. **Mount creation:** the driver creates or reuses the mount process or Mount Pod and invokes the JuiceFS client.
4. **Mount propagation:** the volume is made available to the workload Pod.
5. **Workload I/O:** the application opens, writes, closes, reopens, and reads files through the mounted volume.

A Bound PVC, Running Mount Pod, or successful mount command is not end-to-end acceptance. Verify workload I/O and, where relevant, cross-Pod visibility.

## Read CSI source when behavior is unclear

At the exact deployed revision, start with:

- `pkg/driver/controller.go` and `pkg/driver/provisioner.go` for controller-side provisioning;
- `pkg/driver/node.go` for node publish, unpublish, staging, and cleanup;
- `pkg/juicefs/` and `pkg/juicefs/mount/` for mount orchestration and lifecycle;
- `pkg/juicefs/mount/builder/` for command construction and option precedence;
- `pkg/config/` for configuration parsing and defaults;
- `pkg/controller/` for controller reconciliation;
- `pkg/webhook/` for admission and mutation behavior.

Trace from the Kubernetes or gRPC entry point to the actual JuiceFS client invocation and cleanup path. Cite the revision and files used. See [source-code.md](source-code.md) for the evidence format.

## Collect evidence by stage

- PVC, PV, Pod, and node events for scheduling and lifecycle failures.
- CSI Controller logs for provisioning and deletion failures.
- CSI Node logs for node-side publish, mount, and cleanup failures.
- Mount Pod or sidecar logs for JuiceFS client startup and runtime errors.
- Workload logs and a controlled I/O test for application-visible failures.
- Version-matched JuiceFS CSI diagnostic tools, such as the documented `kubectl jfs` workflow or CSI doctor, when available in that release.

Redact metadata URLs, passwords, object storage keys, Secret data, kubeconfig content, internal hostnames where required, and customer data paths.

## Protect production state

- Verify reclaim policy and business ownership before deleting a PVC, PV, StorageClass, Secret, or backing filesystem.
- Do not restart or delete all Mount Pods as an initial diagnostic step.
- Do not remove cache directories, mount paths, finalizers, or Kubernetes resources without understanding the matching driver cleanup path.
- Confirm CPU, memory, ephemeral storage, cache capacity, scheduling constraints, and node pressure for CSI and Mount Pods.
- Change one variable at a time and preserve before-and-after evidence.

## Acceptance checklist

Confirm at least:

1. Provisioning and scheduling complete without unexplained warning events.
2. The expected mount process or Mount Pod remains healthy after restart scenarios.
3. The workload creates a unique file, closes it, reopens it, reads it, and verifies its checksum.
4. Another Pod or node observes the expected data and permissions when the topology requires it.
5. Metrics and logs can be correlated across workload, Mount Pod, CSI Node, and CSI Controller.
6. Upgrade, rollback, reclaim, and recovery responsibilities are documented.
