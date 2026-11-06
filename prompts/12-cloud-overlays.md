# 12 — Cloud GitOps overlays

Needs: 11 merged, phase 3 working. Status: built on `feat/cloud-overlays` (PR #29); it still needs one passing cloud session (`13-cloud-session.md`).

```text
Cloud GitOps overlays for every stack that exists.
Worktree: git worktree add ../devops-lab-wt-cloud -b feat/cloud-overlays origin/main

1. gitops/overlays/cloud/ for core, obs, sample-app, secrets, sample-db,
   policy. Mode differences only there and in internal/provider; explain
   anything else in the PR.
   - Envoy Gateway as ClusterIP, reached with `lab tunnel` (no LB).
   - Images: ECR instead of Zot, one repository per image; the sample-db
     image built for arm64 (Graviton nodes) and pushed by digest. Every
     third-party image must have an arm64 build.
   - secrets: ESO on real Secrets Manager through EKS Pod Identity, the
     same ClusterSecretStore name (lab-secrets) and the same aws
     commands; secrets tagged and deleted with no recovery window by
     lab down and the reaper.
   - Storage: EBS CSI driver installed by lab (Helm), IAM policy
     arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy (there
     is no "V2"); StorageClass lab-gp3 (gp3, encrypted, Delete); volume
     tags carry expires-at so lab extend can update them.
   - obs: short retention, small requests.
   - Realistic memory REQUESTS on every cloud container, about 25% above
     what `kubectl top` shows on the local lab (Argo CD controller,
     repo-server, cert-manager, metrics-server, ESO, Kyverno,
     kube-prometheus-stack, Loki and its sidecars). Karpenter sizes nodes
     from requests; too-small requests packed Argo CD onto a t4g.micro
     and it crash-looped. A render test fails if any cloud container has
     no memory request.
2. Nodes: VPC CNI prefix delegation on (small nodes otherwise cap at 4–11
   pods, which left node-exporter Pending); DaemonSets get
   priorityClassName system-node-critical; the spot NodePool requires
   more than 4000 MiB of memory (t4g.medium and up) and has a hard cap
   on total CPU.
3. Adding a stack to a running EKS cluster checks that stack's compat
   row against the running version and refuses if unsupported.
4. Charts: one shared cache (~/.lab/helm/charts); prefetch every chart a
   run installs from the host (Argo CD, Karpenter, LBC, EBS CSI) before
   any stack runs; retries with backoff. A missing chart stops lab up in
   seconds with nothing built.
5. Offline tests: render every stack for both modes; a cloud render must
   have no LoadBalancer Service, no Zot or Moto reference, no untagged
   volume, and a memory request on every container.
6. Reaper: delete expired lab secrets (no recovery window); closed spot
   requests and instant fleets count as gone.
7. CLAUDE.md: "The design lives in docs/design.md; update it in the
   same PR as the change."

Then one cloud session (13-cloud-session.md) with
--stacks core,obs,sample-app,secrets,sample-db,policy. The PR carries
every session's timeline, leftovers report and cost.
```
