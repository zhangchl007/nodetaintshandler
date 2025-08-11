# nodetaintshandler

Temporary node startup gate: taint new user nodes, run an initialization DaemonSet, then remove the taint so regular workloads can schedule.

## Overview

1. Mutating webhook ([deploy/deployment.yaml](deploy/deployment.yaml)) calls [`webhook.MutateNode`](pkg/webhook/node_webhook.go) on Node CREATE to add taint  
   `startup.k8s.io/initializing=wait:NoSchedule` (skips AKS system-mode nodes).
2. Critical kube-system daemons tolerate the taint (manually patched once).
3. Initialization DaemonSet ([deploy/startup-daemonset.yaml](deploy/startup-daemonset.yaml)) (label `startup.k8s.io/component=init`) performs per-node warm-up.
4. Controller [`startup.Controller`](pkg/startup/controller.go) watches Nodes & init Pods; when startup Pod is ready (annotation or PodReady + all containers Ready) it removes the taint via [`startup.removeStartupTaint`](pkg/startup/controller.go).
5. Workload Pods can then schedule. Completion time is recorded via annotation.

Core symbols: [`webhook.MutateNode`](pkg/webhook/node_webhook.go), [`startup.Controller`](pkg/startup/controller.go), [`startup.HasStartupTaint`](pkg/startup/controller.go).

## Taint / Labels / Annotations

- Taint key/value: `startup.k8s.io/initializing=wait:NoSchedule` (constants: [`startup.TaintKey`](pkg/startup/constants.go), [`startup.TaintValue`](pkg/startup/constants.go))
- Init Pod label selector: `startup.k8s.io/component=init` (see [`startup.StartPodLabelKey`](pkg/startup/constants.go), [`startup.StartPodLabelValue`](pkg/startup/constants.go))
- Optional readiness shortcut annotation: [`startup.StartPodReadyAnnotation`](pkg/startup/constants.go) (`"true"` to force early taint removal)
- Node completion annotation: [`startup.NodeStartupCompletedAnnotation`](pkg/startup/constants.go)

## Environment Variables

- `STARTUP_WEBHOOK=1` enable webhook HTTP server (insecure demo on :8443)
- `STARTUP_BACKFILL=1` controller backfills the startup taint onto existing / missed nodes that:
  - are not already tainted
  - have no completion annotation
  - have no non-system workloads (checked by [`startup.hasWorkloadPods`](pkg/startup/controller.go))

## Build & Test

```sh
go build ./...
go test ./... -cover
```

Make targets:

```sh
make build
make test
make docker-build
make docker-push
```

## Run (Out of Cluster)

Requires kubeconfig & RBAC (see [deploy/deployment.yaml](deploy/deployment.yaml)). Disable in-cluster config logic if adapting for local dev.

## Deploy (Cluster)

1. Adjust image reference in [deploy/deployment.yaml](deploy/deployment.yaml).
2. Apply manifests:
   ```sh
   kubectl apply -f deploy/deployment.yaml
   kubectl apply -f deploy/startup-daemonset.yaml
   ```
3. (Prod) Provide TLS cert/key via Secret & update webhook server to use HTTPS.
4. Patch critical system DaemonSets with toleration:
   ```sh
   kubectl -n kube-system patch ds kube-proxy --type=json \
     -p='[{"op":"add","path":"/spec/template/spec/tolerations/-","value":{"key":"startup.k8s.io/initializing","operator":"Equal","value":"wait","effect":"NoSchedule"}}]'
   ```

## AKS System / User Pools

Webhook skips nodes labeled `kubernetes.azure.com/mode=system` (see constant `aksModeLabel` in [`webhook.MutateNode`](pkg/webhook/node_webhook.go)) so system pool scheduling is unaffected.

## Controller Logic

- Watches Nodes & Pods via shared informers (30s resync).
- For each tainted node, calls [`startup.startupPodReady`](pkg/startup/controller.go) to evaluate readiness:
  - Annotation shortcut OR PodReady=True AND all containers Ready.
- On success: [`startup.removeStartupTaint`](pkg/startup/controller.go) updates Node & adds completion annotation.

## Backfill Strategy

When enabled (`STARTUP_BACKFILL=1`) [`startup.backfillTaint`](pkg/startup/controller.go):
- Lists nodes
- Skips already tainted or completed
- Skips nodes with workload pods (non kube-system/kube-public)
- Adds taint (so init DaemonSet can run) without disrupting existing workloads.

## Testing

Focused unit tests:
- Controller behaviors (readiness paths, removal) in [pkg/startup/controller_test.go](pkg/startup/controller_test.go)
- Webhook patch logic in [pkg/webhook/node_webhook_test.go](pkg/webhook/node_webhook_test.go)
- Handler helper in [pkg/startup/handler_helpers_test.go](pkg/startup/handler_helpers_test.go)

## Image Build

```sh
docker build -t <repo>/nodetaintshandler:latest .
docker push <repo>/nodetaintshandler:latest
```

## Hardening / Production TODO

- Serve webhook over TLS (MutatingWebhookConfiguration CA bundle)
- AuthN / narrow RBAC
- Metrics & structured logging
- Optional Pod webhook to auto-inject tolerations (if desired)
- Exponential backoff / retry around Node updates

##