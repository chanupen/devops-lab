# A release that crashes on start

The pager goes off: the sample app's frontend at https://podinfo.localtest.me:8443 returns errors since a few minutes ago. Nobody merged anything to Git today. Find what broke, roll it back the way your team deploys (Argo CD), and run `lab drills verify`.

## What happened

A CD pipeline released podinfo 6.15.1 to `podinfo-frontend` by setting Helm parameters on its Argo CD Application (`image.repository`, `image.tag`), the way Argo CD Image Updater's `argocd` write-back does. Git did not change. The new image's start script passes `--port-metrics=9898`, the port the HTTP server already uses, so the process exits at once:

```text
HTTP server crashed  error="listen tcp :9898: bind: address already in use"
```

The kubelet restarts it with a growing back-off: `CrashLoopBackOff`. The podinfo chart runs one replica with a rolling update, so the old pod was already gone: the outage is total.

## Finding it

- `kubectl -n podinfo get pods` shows the frontend pod not Ready, in `CrashLoopBackOff`, with restarts.
- `kubectl -n podinfo describe pod` shows the image (`lab-zot:5000/lab/podinfo:6.15.1`), the last state (`Error`, exit code 1) and the back-off events.
- `kubectl -n podinfo logs <pod> --previous` shows why the process exited.
- `argocd app history podinfo-frontend` shows a new release with the parameters; `argocd app get podinfo-frontend -o yaml` shows them on the Application.
- The alert: `KubePodCrashLooping`, from kube-prometheus-stack. Upstream waits 15 minutes; the lab's obs stack waits 5, so it fires while you work.

## The fix: roll back through Argo CD

An emergency rollback, then the root cause in the release process:

```bash
argocd app history podinfo-frontend              # find the last good release
argocd app set podinfo-frontend --sync-policy none   # Argo CD refuses to roll back while auto-sync is on
argocd app rollback podinfo-frontend <id>
```

Why not `kubectl rollout undo` or `kubectl edit`? Argo CD owns the Deployment. With self-heal on it puts the bad release back within seconds; with auto-sync off, the cluster and Argo CD disagree, and the next sync brings the bad release back. A rollback through Argo CD records what runs and why in its history.

When the history has no good release to go back to, undo the release itself, still through Argo CD: `argocd app unset podinfo-frontend -p image.repository -p image.tag` removes the parameters the pipeline set, and Argo CD syncs the release in Git. `lab drills verify` accepts either; it checks that the release running came from Argo CD.

After a rollback the app shows `OutOfSync`: it runs the old release while its desired state still names the new one. That is expected after an emergency rollback. The lasting fix is in the release process: fix or revert the release where it came from (the pipeline, or Git when the image tag is in Git), then turn auto-sync back on. When the drill ends, lab does that part: it puts the Application back as Git has it.

`argocd` here means the CLI in core mode: it talks to the API server with your kubeconfig, no Argo CD login. Core mode reads its namespace from the kube context, so point the context at `argocd` once (kubectl then defaults to that namespace too):

```bash
export KUBECONFIG=~/.lab/kubeconfig ARGOCD_OPTS=--core
kubectl config set-context --current --namespace argocd
```

Or use the UI at https://argocd.localtest.me:8443 (History and Rollback).

## Why the parent app lets you pause auto-sync

`lab-bootstrap` renders every Application from Git and self-heals them. During this drill it ignores two fields of `podinfo-frontend` (`ignoreDifferences` with `RespectIgnoreDifferences=true`): the Helm parameters a CD tool writes, and the auto-sync switch. That is the usual setup for an app-of-apps whose child apps are released by a tool, and it is what lets you pause auto-sync in an incident. lab sets it before the fault and removes it afterwards.
