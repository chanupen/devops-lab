# 91 — Known issues and lessons

Problems hit while building the lab, and the fix for each. Check here first when something breaks.

| Symptom | Cause | Fix |
| --- | --- | --- |
| ghcr.io pulls or chart downloads crawl or time out | GitHub's CDN (`pkg-containers.githubusercontent.com`, `release-assets.githubusercontent.com`, 185.199.108–111.x) is slow or one address times out from this network | `lab doctor` measures it; `zot.addHosts` in `lab.yaml` pins a good address inside lab-zot only; charts are prefetched into the shared cache with retries before anything is built |
| `lab up` refuses: cluster runs another Kubernetes version | kind clusters can't change version | `lab down`, then `lab up`; the Zot cache volume is kept |
| Version check passed on the wrong version | kind's Podman provider pulls by digest, so the image name has no tag | Read `/kind/version` in the node; never parse the image name |
| `lab` says the lab is pinned to an old version after `git pull` | The installed binary is old | `make install` after every pull |
| Argo CD `lab-bootstrap` Unknown, `Permission denied (publickey)` | Short GitHub-side failure, or the repo Secret is stale | Test the key with `ssh -i ~/.ssh/devops-lab-deploy -T git@github.com`; `lab up --stacks gitops --refresh-repo`; hard refresh the app |
| Argo CD `lab-bootstrap` Unknown, `unable to resolve ... to a commit SHA` | The GitOps revision tracks a branch that was deleted after merge | Plain `lab up` moves GitOps back to `main`; after testing a branch, always move it back before finishing |
| Kyverno app stays OutOfSync | Chart CRDs ship empty labels/annotations and are too big for client-side apply | `ignoreDifferences` on CRD metadata and `ServerSideApply=true` |
| `profile lab: credential type source_profile requires role_arn, profile devops` | Something added `source_profile = devops` to `[profile devops]` (likely `claws`) | Remove that line; `[profile devops]` holds only region and output |
| Cloud apply failed: `AmazonEBSCSIDriverPolicyV2 does not exist` | Wrong policy name | `service-role/AmazonEBSCSIDriverPolicy`; check every managed policy ARN before apply |
| node-exporter Pending, `Too many pods` | VPC CNI without prefix delegation caps small nodes at 4–11 pods | Prefix delegation on; DaemonSets `system-node-critical` |
| Argo CD repo-server crash-looping on cloud | Small memory requests let Karpenter pack it onto a t4g.micro | Realistic requests (+25% over measured); NodePool minimum 4 GiB |
| A failed cloud `lab up` left the lab billed until the TTL | Nothing ran `lab down` | Red "still billed" message, non-zero exit; `lab down` never waits on app health |
| `lab cloud leftovers` lists instances, volumes, fleets after down | The tagging API lags up to an hour | Each entry is checked with Describe; terminated, not found, closed spot requests and instant fleets are listed as stale and ignored |
| An interrupted drill e2e left a fault | Its state lived in a temp file | e2e state in `~/.lab/e2e/state.json`; `lab doctor` reports it and `lab up` restores it |
| Two agent sessions changed each other's branches | They shared one checkout or worktree | One worktree per session, never `~/devops-lab` |
| A new agent session didn't know a cloud session was approved or running | Context lost between sessions | Start with `13-cloud-session.md` rule 6: check AWS first; paste the plan again |
| A 223 MB binary almost got committed | `git add -A` with a binary in the repo root | `/lab` and `bin/` in `.gitignore`; check `git status` before committing |
| LocalStack won't start without a token | LocalStack 2026.03 needs an auth token | Moto replaces it (see the decision in `docs/design.md`) |
| Lab 1's Argo CD chart download timed out | One GitHub release address times out from this host | Install from `oci://ghcr.io/argoproj/argo-helm` |
| `lab labs stop` kept the time and steps | Fixed in #19 | Time counts only while the cluster runs; stop resets a scratch lab |
| Ollama answered on the LAN address (192.168.x.x:11434) with no auth | The Quadlet published `11434:11434` with no host IP, so pasta bound `*:11434`, and the FedoraWorkstation zone opens 1025–65535/tcp | `PublishPort=127.0.0.1:11434:11434`; `lab doctor` (lan listeners) fails on any lab port off loopback |
| Pods cannot reach Ollama through `host.containers.internal` | pasta maps 169.254.1.2 to the host's own address, not its loopback | Ollama's Quadlet joins the kind network at a fixed address (`Network=kind:ip=...`); `ollama.ai.svc` points there |
| An EndpointSlice in Git is never applied | Argo CD excludes Endpoints and EndpointSlices by default | lab writes `ollama-host` itself; the Service stays in Git |
| `kubectl get --raw .../services/ollama:11434/proxy/...` says the Service is not found | The API server's service proxy reads Endpoints, not EndpointSlices | Check from a node through the ClusterIP (`lab doctor` does) |
| Langfuse returns 400 "Event type not accepted" for `trace-create` | Langfuse v4 runs `events_only`: traces come over OTLP only | Send to `/api/public/otel/v1/traces`; read with `/api/public/v2/observations` |
