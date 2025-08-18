# nodetaintshandler

Gate scheduling on freshly created user nodes: add a temporary taint at Node creation, run a per‑node initialization DaemonSet, then remove the taint so regular workloads can schedule.

## Why

Warm up images / caches / extensions (or run validation) before user workloads land, without permanently reserving resources or modifying every workload spec.

---

## Core Flow

1. Mutating webhook ([deploy/deployment.yaml](deploy/deployment.yaml)) invokes [`webhook.MutateNode`](pkg/webhook/node_webhook.go) on Node CREATE and patches taint  
   `startup.k8s.io/initializing=wait:NoSchedule` (skips AKS system pool nodes labeled `kubernetes.azure.com/mode=system`).
2. Only DaemonSets (and any system components you explicitly patch) that tolerate the taint start.
3. The init DaemonSet Pod ([deploy/startup-daemonset.yaml](deploy/startup-daemonset.yaml)) labeled `startup.k8s.io/component=init` performs warm‑up.
4. Controller [`startup.Controller`](pkg/startup/controller.go) watches Nodes & Pods. Readiness logic: [`startup.startupPodReady`](pkg/startup/controller.go) (annotation shortcut or all containers Ready + PodReady).
5. When complete it removes the taint via [`startup.removeStartupTaint`](pkg/startup/controller.go) and writes completion annotation.
6. Workload Pods (no toleration) can now schedule.

---

## Key Symbols

| Symbol | Purpose |
|--------|---------|
| [`webhook.MutateNode`](pkg/webhook/node_webhook.go) | JSONPatch Node CREATE to add taint |
| [`startup.Controller`](pkg/startup/controller.go) | Informer-driven reconciler |
| [`startup.HasStartupTaint`](pkg/startup/controller.go) | Helper to detect taint presence |
| [`startup.startupPodReady`](pkg/startup/controller.go) | Determines if init Pod finished |
| [`startup.removeStartupTaint`](pkg/startup/controller.go) | Removes taint + annotates Node |
| Constants: [`startup.TaintKey`](pkg/startup/constants.go), [`startup.TaintValue`](pkg/startup/constants.go), [`startup.StartPodLabelKey`](pkg/startup/constants.go), [`startup.StartPodLabelValue`](pkg/startup/constants.go), [`startup.StartPodReadyAnnotation`](pkg/startup/constants.go), [`startup.NodeStartupCompletedAnnotation`](pkg/startup/constants.go) | Contract for taint/labels/annotations |

---

## Taints / Labels / Annotations

| Item | Value / Format | Set By |
|------|----------------|--------|
| Startup taint | `startup.k8s.io/initializing=wait:NoSchedule` | Webhook |
| Init Pod label | `startup.k8s.io/component=init` | DaemonSet template |
| (Optional) Early ready annotation | `startup.k8s.io/ready=true` | Init Pod logic |
| Node completion timestamp | `startup.k8s.io/completedAt=<unixEpoch>` | Controller |

---

## Environment Variables

| Var | Effect |
|-----|--------|
| `STARTUP_WEBHOOK=1` | (Currently always started) serve webhook HTTPS |
| `STARTUP_BACKFILL=1` | [`startup.backfillTaint`](pkg/startup/controller.go) retro-taints idle untainted nodes (no user pods) |

(Strict/hold options like annotation‑only or min hold time are not yet in code unless you extend it.)

---

## Project Layout

```
main.go
pkg/
  webhook/ (mutation handler)
  startup/ (controller, constants, helpers, tests)
deploy/ (Kubernetes manifests & cert helper)
Dockerfile
Makefile
```

---

## Build & Test

```sh
go build ./...
go test ./... -cover
```

Or via Make:

```sh
make build
make test
```

---

## Container Image

```sh
make docker-build DOCKER_TAG=v1.6
make docker-push DOCKER_TAG=v1.6
```

---

## Deployment (Cluster)

1. Generate TLS certs & patch CA bundle (updates Secret + MutatingWebhookConfiguration):

   ```sh
   cd deploy
   ./generate_webhook_certs.sh \
     --namespace kube-system \
     --service node-startup-webhook \
     --secret node-startup-webhook-tls \
     --webhook node-startup-taint
   ```

2. Edit [deploy/deployment.yaml](deploy/deployment.yaml):
   - Set image `yourrepo/nodetaintshandler:<tag>`
   - (Optional) Adjust `failurePolicy` (currently `Fail` for strict gating)
   - Add env `STARTUP_BACKFILL=1` if you want missed nodes tainted (only when idle)

3. Apply controller + webhook:

   ```sh
   kubectl apply -f deploy/deployment.yaml
   ```

4. Apply init DaemonSet:

   ```sh
   kubectl apply -f deploy/startup-daemonset.yaml
   ```

   Customize its script to perform real warm‑up. Add a readiness probe or set the annotation when done if you modify logic.

5. (Optional) Patch only necessary system DaemonSets to tolerate the taint (avoid broad patch unless needed):

   ```sh
   kubectl -n kube-system patch ds kube-proxy --type=json \
     -p='[{"op":"add","path":"/spec/template/spec/tolerations/-","value":{"key":"startup.k8s.io/initializing","operator":"Equal","value":"wait","effect":"NoSchedule"}}]'
   ```

6. Verify:

   ```sh
   kubectl get nodes -o custom-columns=NAME:.metadata.name,TAINTS:.spec.taints | grep initializing
   kubectl logs -n kube-system deploy/nodetaintshandler | grep "Adding startup taint"
   ```

7. Observe taint removal:

   ```sh
   kubectl logs -n kube-system deploy/nodetaintshandler | grep "Removed startup taint"
   ```

---

## Testing Node Autoscale

Deploy a workload (e.g. [deploy/nginx-deploy.yaml](deploy/nginx-deploy.yaml)) to trigger scaling:

```sh
kubectl apply -f deploy/nginx-deploy.yaml
```

Watch new nodes get tainted and held until the init Pod readiness condition.

---

## Unit Tests

- Webhook patch cases: [pkg/webhook/node_webhook_test.go](pkg/webhook/node_webhook_test.go)
- Controller readiness & removal paths: [pkg/startup/controller_test.go](pkg/startup/controller_test.go)
- Event handler helper: [pkg/startup/handler_helpers_test.go](pkg/startup/handler_helpers_test.go)

Run:

```sh
go test ./... -cover
```

---

## Troubleshooting

| Symptom | Likely Cause | Remedy |
|---------|--------------|--------|
| Workloads schedule before init Pod | Node missed mutation (webhook unavailable) or taint removed quickly | Ensure webhook Pod Ready before scaling; keep `failurePolicy: Fail`; add readiness gating in init Pod |
| Taint never removed | Init Pod never reaches Ready condition / annotation | Add readinessProbe or set annotation; inspect Pod status |
| Backfill skipped node | Node already has user Pods | Manually decide if retro-taint is safe |
| Webhook 404 / probe failing | TLS secret not mounted yet | Secret projection delay – startup code already waits; check logs |

Check failed webhook calls:

```sh
kubectl get events -A --sort-by=.lastTimestamp | grep -i webhook
```

---

## Extending

Ideas:
- Require explicit annotation only (remove implicit PodReady path).
- Minimum taint hold time.
- Two‑phase taints (preinit -> warming).
- Validating webhook to block Pod admission if taint present without toleration.
- Metrics & structured logging (Prometheus / JSON).
- RBAC hardening (split read vs patch).

---

## Cleanup

```sh
kubectl delete -f deploy/startup-daemonset.yaml
kubectl delete -f deploy/deployment.yaml
```

---

## Limitations

- No guarantee init DaemonSet Pod becomes the very first Pod (race with other tolerated DS).
- Without readinessProbe or annotation the init Pod may be “Ready” immediately (sleep).
- Backfill only covers idle nodes (avoids disrupting active ones).

---

## License

MIT License

---