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

## Writing a lab

- Each step teaches one thing.
- Short sentences, plain words, no company names.
- Pin every version: charts, images, Git tags or commits.
- The solutions must pass the verify scripts on a fresh cluster. `go test -tags e2e ./internal/labs/` checks that, on a cluster named `lab-scratch-e2e-<NN>-<pid>`, so it never meets a learner's.
- `lab labs start` never reuses a scratch cluster that its state did not start; it says how to delete it instead.
- Time spent counts only while the lab's cluster runs.
- `lab labs stop` prints how far the lab got ("stopped at step 4/6 after 32m"). For a scratch lab it deletes the cluster and resets the steps and the time, so the next start begins at step 1. A lab on the main cluster keeps its steps; its time starts from zero.
