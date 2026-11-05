# Labs

Hands-on labs, one directory each. `lab labs list` shows them, and `lab labs start <id>` starts one.

## Format

```text
labs/NN-name/
  lab.yaml          id, title, time, level, modes, needs, stacks, tools
  README.md         an intro, then "## Step N: title" sections
  steps/NN-verify.sh  one per step
  reset.sh          returns the cluster to the lab's start
  solution/NN-solve.sh  a reference answer per step, plus any files it applies (NN-*)
```

`lab.yaml`:

| Field | Meaning |
|---|---|
| `id` | `NN-name`, the same as the directory |
| `title`, `time`, `level` | shown in the list |
| `modes` | `local`, `cloud` |
| `needs` | `scratch`: the lab gets its own single-node cluster, `lab-scratch-<id>`. `lab`: it runs on the main lab's cluster. |
| `stacks` | for `needs: lab`, the main-lab stacks it needs |
| `tools` | commands the steps use; `lab labs start` checks they are on PATH |
| `install` | a pinned install command per tool and platform (`linux/amd64`, `darwin/amd64`), printed when the tool is missing |

Each step in the README has three parts: the task, `### Hints` (a list, one hint each) and `### Expected result`.

## Verify scripts

- They are read-only and idempotent: they look, they never change anything.
- They run with `KUBECONFIG` set to the lab's cluster, `LAB_ID`, `LAB_DIR` and `LAB_STEP`, and a 60-second timeout.
- Exit 0 means the step is done. Otherwise the last line printed says why not, in one short sentence.

## Labs on the main cluster (`needs: lab`)

- Work only in the lab's own namespaces, `labNN-*` (for example `lab02-dev`). Label every cluster-scoped object the lab creates `lab.devops/lab: <id>`. `reset.sh` removes exactly those, and nothing else.
- The scripts get `LAB_REGISTRY_PUSH` (where the host pushes: `127.0.0.1:5000/lab` in local mode) and `LAB_REGISTRY_PULL` (how the cluster pulls: `lab-zot:5000/lab`). In cloud mode both are `<account>.dkr.ecr.ap-south-1.amazonaws.com/lab`; ECR makes no repository on push, so every image name a lab pushes must be in the registry module's `repositories` list. Use them instead of a registry name, so cloud mode needs only an overlay (ECR).
- A solution that keeps files on the host uses `LAB_WORK` when it is set (the e2e test sets a temporary directory), else a directory the README names, such as `~/lab02`.
- Tools beyond `kubectl`, `jq` and `curl` run as images pinned by digest through rootless Podman, so a learner installs nothing. The README gives a shell function for each, for example `trivy() { podman run --rm --network host ... "$@"; }`.
- A verify script that reads a rate over a window may still pass for a while after reset. The e2e test waits for it to fail before running the solution.

## Writing a lab

- Each step teaches one thing.
- Short sentences, plain words, no company names.
- Pin every version: charts, images, Git tags or commits.
- The solutions must pass the verify scripts on a fresh cluster. `go test -tags e2e ./internal/labs/` checks that: scratch labs on a cluster named `lab-scratch-e2e-<NN>-<pid>`, so it never meets a learner's; labs on the main cluster with `LAB_E2E_MAIN_KUBECONFIG=~/.lab/kubeconfig`, inside their own namespaces.
- `lab labs start` never reuses a scratch cluster that its state did not start; it says how to delete it instead.
- Time spent counts only while the lab's cluster runs.
- `lab labs stop` prints how far the lab got ("stopped at step 4/6 after 32m"). For a scratch lab it deletes the cluster and resets the steps and the time, so the next start begins at step 1. A lab on the main cluster keeps its steps; its time starts from zero.
