# nodetaintshandler (Startup Gate + System Taint Controller)

This project applies a temporary startup taint to new (user) nodes, blocks workload Pods until an initialization DaemonSet finishes, then removes the taint.

## Core Workflow

1. Node CREATE mutating webhook ([deploy/MutatingWebhook-nodes .yaml](deploy/MutatingWebhook-nodes%20.yaml)) calls [`webhook.MutateNode`](pkg/webhook/node_webhook.go) to add taint `startup.k8s.io/initializing=wait:NoSchedule` (skips AKS system-mode nodes).
2. kube-system critical DaemonSets are pre-configured (manually) with a toleration:
   ```yaml
   tolerations:
   - key: startup.k8s.io/initializing
     operator: Equal
     value: wait
     effect: NoSchedule
   ```
3. Startup DaemonSet ([deploy/startup-daemonset.yaml](deploy/startup-daemonset.yaml)) (label `startup.k8s.io/component=init`) runs per node performing warm-up.
4. Controller ([`startup.Controller`](pkg/startup/controller.go)) detects the init Pod ready (annotation or PodReady) and removes the taint.
5. Workload Pods begin scheduling.

## Features

- Node taint on creation
- Manual (once) toleration addition to required kube-system DaemonSets (no Pod webhook)
- Clean taint removal with completion annotation
- Optional backfill (`STARTUP_BACKFILL=1`)
- AKS system/user pool awareness

## Add Tolerations

Patch key DaemonSets (examples):
```sh
kubectl -n kube-system patch ds kube-proxy --type='json' -p='[{"op":"add","path":"/spec/template/spec/tolerations/-","value":{"key":"startup.k8s.io/initializing","operator":"Equal","value":"wait","effect":"NoSchedule"}}]'
```

Referenced symbols: [`webhook.MutateNode`](pkg/webhook/node_webhook.go), [`startup.Controller`](pkg/startup/controller.go), [`startup.HasStartupTaint`](pkg/startup/controller.go)