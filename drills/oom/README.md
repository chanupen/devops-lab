# A memory limit far below what the app needs

The pager goes off: the sample app's frontend at https://podinfo.localtest.me:8443 fails its calls to the backend, on and off, since the backend team's latest release. They want to keep that release. Find what is wrong, fix it the GitOps way, and run `lab drills verify`.

## What happened

The backend team released `podinfo-backend` with an in-memory catalogue: at start it loads 128 MB of data (`podinfo --stress-memory=128`). The release came through Argo CD as a Helm parameter (`extraArgs`), like a CD tool with Argo CD write-back would set it. Nobody changed the memory settings, which Git keeps at a request of 16Mi and a limit of 64Mi. The process needs about 140Mi, so it hits the cgroup's limit while it loads the data, and the kernel's OOM killer ends it:

```text
Last State:  Terminated
  Reason:    OOMKilled
  Exit Code: 137
```

The kubelet restarts it, it is killed again, and the pod ends up in `CrashLoopBackOff`.

## Finding it

- `kubectl -n podinfo get pods` shows the backend restarting.
- `kubectl -n podinfo describe pod` shows `OOMKilled` and exit code 137 (128 + SIGKILL) in the container's last state, and the limits.
- The container's command has `--stress-memory=128`: the release needs more memory than before.
- The need: Prometheus's `container_memory_working_set_bytes{namespace="podinfo"}` (Grafana, Explore) keeps the peak; `kubectl top` sees a pod only while it lives.
- The alert: `KubeContainerOOMKilled`. kube-prometheus-stack has none for OOM kills, so this drill adds one to the obs stack (`lab-drills` rules): a container restarted in the last 10 minutes and its last termination was `OOMKilled`.

## The fix: right-size the memory in the desired state

```bash
argocd app set podinfo-backend -p resources.requests.memory=160Mi -p resources.limits.memory=256Mi
```

Argo CD rolls it out. `kubectl edit` or `kubectl set resources` would work for a few seconds: self-heal puts the Deployment back to the desired state. In a repo you own, this is a commit to the values file; here the release lives in the Application, so the fix goes there too.

`lab drills verify` wants a request of at least 128Mi, a limit between 160Mi and 1Gi, the release kept, the app Synced and no restart since.

## Requests and limits

- **The request** is what the scheduler reserves on a node. Too low, and the node is overcommitted: pods that use more than they asked for are the first evicted when the node runs short. Set it near what the app uses normally.
- **The limit** is the hard cap. Past it the kernel kills the process; there is no slow-down for memory, unlike CPU, which is throttled. Set it above the peak with headroom (here about 140Mi at peak, so 160Mi to 256Mi).
- **Request equal to limit** gives the Guaranteed QoS class, evicted last. A limit far above the request lets one leaking pod take memory its neighbours were promised.
- **No limit** means a leak can take the node's memory. Keep a cap, and alert on OOM kills, so a limit that is too low shows up as an alert, not as a mystery.
