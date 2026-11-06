# 07 — Labs framework and Lab 1

Needs: phase 2 merged (the TUI's Labs screen), local core working.

```text
Labs framework and Lab 1 (Bootstrap Argo CD from scratch).

Framework (internal/labs, labs/README.md):
- labs/NN-name/: lab.yaml (id, title, time, level, modes, needs:
  scratch | lab, stacks, tools, install: a pinned install command per
  tool and platform with checksums), README.md with "## Step N: title"
  sections (task, ### Hints, ### Expected result), steps/NN-verify.sh,
  reset.sh, solution/NN-solve.sh (+ files).
- Verify scripts are read-only and idempotent; KUBECONFIG, LAB_ID,
  LAB_DIR, LAB_STEP set; 60 s timeout; exit 0 = done, else the last line
  says why not in one short sentence.
- needs: scratch gets its own single-node kind cluster
  lab-scratch-<id> on the kind network, pulling through Zot, no host
  ports, kubeconfig at ~/.lab/labs/<id>/kubeconfig (never ~/.kube/config),
  same pinned node image as the main lab.
  - start refuses a cluster with that name that its state did not start;
    the e2e test uses lab-scratch-e2e-<NN>-<pid>.
  - start starts lab-zot and a stopped scratch node if they exist.
  - A failed start deletes only a cluster it created, and records the lab
    as started only after the cluster is confirmed there.
- CLI: lab labs list | start <id> (the number is enough) | status |
  verify [step] | hint [step] | solution <step> | reset | stop.
  list shows the current step ("step 2/6, 12m"), not "started".
- Time counts only while the lab's cluster runs (node start/stop times
  from Podman); may undercount, never overcounts.
- start prints 5–6 progress lines and the step; kind's output goes to the
  op log; -v streams it.
- stop: for a scratch lab deletes the cluster and resets steps and time
  ("stopped at step 4/6 after 32m"); a lab on the main cluster keeps its
  steps.
- A missing tool prints its pinned install command.
- TUI screen 3: enter opens the step, v verifies, h next hint, n/p move.
- e2e (go test -tags e2e ./internal/labs/): every solution passes its
  verify on a fresh cluster; each verify fails before its solution.

Lab 1 (needs: scratch, 45m, beginner), six steps: install argo-cd chart
(pinned, from oci://ghcr.io/argoproj/argo-helm) into argocd; log in with
the argocd CLI; create an Application for podinfo; break and watch
self-heal; sync options; an app-of-apps root. argocd CLI pinned with its
checksum.

Work through Lab 1 once as a learner and fix anything unclear.
```
