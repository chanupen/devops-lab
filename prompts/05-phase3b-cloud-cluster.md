# 05 — Phase 3b: cloud cluster

Needs: 04 merged and its dead man's switch test passed. Gate: no leftovers after `lab down`.

```text
infra/terraform/aws/cluster (VPC, EKS, Karpenter IAM, Pod Identity) and
infra/terraform/aws/cluster/registry (ECR); state under cluster/ in the
state bucket. internal/provider/aws runs them with terraform-exec.

1. Cost rules: public subnets, no NAT gateway, no load balancer by
   default, no control-plane logging, no Container Insights, 20 GB gp3
   root disks, short log retention. One small on-demand system node
   (Graviton, arm64) for Karpenter; everything else on Karpenter spot.
2. EKS version: any version in standard support (upgrade_policy
   STANDARD). lab picks the newest version that every pinned component
   supports, from internal/compat (Karpenter, Load Balancer Controller,
   Argo CD, ...); only rows of active stacks count. A running cluster
   keeps its version; cloud.k8sVersion pins one.
3. lab installs Karpenter and the AWS Load Balancer Controller itself
   (Helm SDK, charts from the shared cache), because they must exist
   before GitOps and carry expires-at.
4. TTL: `lab up --mode cloud --ttl 2h` (default 2h, never more than 8h
   ahead). Every resource tagged expires-at. `lab extend 1h` retags
   everything and rewrites the teardown bundles. The TUI warns at 30
   and 10 minutes.
5. `lab up --mode cloud` refuses unless the reaper heartbeat is recent
   and clean and no teardown build runs. Before applying, upload the
   teardown bundles.
6. `lab tunnel`: port-forwards 127.0.0.1:8080/8443 to the Envoy Gateway
   Service, so cloud apps keep the https://<app>.localtest.me:8443 URLs.
7. `lab down`: a whole-environment down never waits on app health. Stop
   Argo CD, delete LoadBalancer Services and PVCs and wait for their
   ENIs and volumes to go, remove Karpenter nodes, then tofu destroy
   cluster and registry. Afterwards `lab cloud leftovers`: everything
   still tagged lab=true outside the baseline, each looked up with the
   reaper's Describe check; stale index entries listed apart and
   ignored.
8. A failed cloud lab up prints, in red, "the cloud lab is half built
   and still billed; run now: lab down --mode cloud" and exits non-zero.
   lab status says the same.
9. A cloud lab keeps its own state, kubeconfig and expiry under
   ~/.lab/cloud/, beside a local lab; the lab lock is shared.
10. Fast-fail on cloud applies: a container in CrashLoopBackOff after 3
    restarts, a node NotReady for 3 minutes, or a pod unschedulable for
    3 minutes stops the apply with the reason.

Run one cloud session with 13-cloud-session.md: lab up (cluster,
registry, gitops, core), lab tunnel, lab down, leftovers empty.
```
