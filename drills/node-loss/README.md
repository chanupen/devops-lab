# A worker node is gone

The pager goes off: https://podinfo.localtest.me:8443 fails for some users, then for all of them. Nobody deployed anything. Find what broke, bring the service back, then explain why one failure took it down and what should have prevented that. Run `lab drills verify` when it is fixed.

## What happened

One of the lab's two kind worker nodes stopped: its container (`podman stop`) went away, as a VM or a physical host does when it crashes or loses power. lab picks the worker that runs `podinfo-frontend`, and avoids the one that runs Prometheus and Alertmanager when it can.

What follows is how Kubernetes handles a lost node:

1. The kubelet stops posting status. After `node-monitor-grace-period` (about 40 s) the node controller marks the node `Ready=Unknown` (`NotReady`) and taints it `node.kubernetes.io/unreachable`.
2. The pods on it are not touched yet. They show as `Running` but not Ready, so their Services stop sending them traffic. With one replica, the outage is total.
3. Each pod tolerates the taint for 300 s (`tolerationSeconds`, added by the DefaultTolerationSeconds admission plugin). Then the node controller evicts them: they go to `Terminating`, which cannot finish while the kubelet is gone, and their Deployments create new pods on the other worker. StatefulSet pods (Postgres) wait: a StatefulSet does not make a second `postgres-0` while the first may still run, and its volume is on the lost node anyway.
4. Everything else on that node goes with it: in this lab, parts of Argo CD, cert-manager, Kyverno or the monitoring stack. A webhook whose only pod ran there (Kyverno's, `failurePolicy: Fail`) can block the very pods that would replace the lost ones until it is rescheduled itself.

## Finding it

- The alert: `KubeNodeNotReady` (kube-prometheus-stack). Upstream waits 15 minutes; the lab's obs stack waits 2. If Prometheus or Alertmanager ran on the lost node, the alert comes only after they are rescheduled: monitoring on the node it watches is a finding of its own.
- `kubectl get nodes`: one worker `NotReady`. `kubectl describe node <name>`: "Kubelet stopped posting node status", the unreachable taints.
- `kubectl -n podinfo get pods -o wide`: the pods on that node not Ready, later `Terminating`, with new ones elsewhere.
- `podman ps -a --filter label=io.x-k8s.kind.cluster=lab`: the node's container has exited.

## The fix

Bring the node back and check it rejoins:

```bash
podman start lab-worker2                 # the stopped one
kubectl get nodes -w                     # Ready within seconds
kubectl -n podinfo get pods -o wide      # Terminating pods finish; the app serves
```

Then the part that prevents the next outage. One node failing should not take a service down:

- **Replicas.** The podinfo apps run one replica each, so losing its node is a full outage until eviction and rescheduling (five minutes or more). Run at least two.
- **Spread.** Two replicas on the same node fail together. Use `topologySpreadConstraints` on `kubernetes.io/hostname` (or zone in the cloud), or pod anti-affinity, so replicas land on different nodes.
- **A PodDisruptionBudget does not help here.** A PDB limits voluntary disruptions, the evictions that go through the Eviction API: `kubectl drain`, a cluster upgrade, Karpenter consolidation. A crashed node is involuntary; no PDB is asked. A PDB matters while you repair: it stops a drain of a second node from taking the last replica.
- **Faster failover**, if five minutes is too long: a shorter `tolerationSeconds` for `node.kubernetes.io/unreachable` on the app's pods, readiness probes that fail fast, and enough spare capacity on the other nodes for the pods that move.
- **Monitoring off the failing node**: alerting that runs on the node it watches cannot page for that node.

## What lab puts back

Restore starts every stopped kind node of the lab, waits until all nodes are Ready, then until every Argo CD app is Synced and Healthy and the podinfo apps are rolled out. It never leaves a node stopped. A pod that moved to the other worker stays there: placement is the scheduler's.
