# 00 — Standing rules for the agent

Send this first in every new session, before any task prompt. It holds for every prompt that follows.

```text
You are building devops-lab: a DevOps/SRE home lab with two modes (local
on Fedora, cloud on AWS), driven by one Go binary called `lab` (a k9s-style
TUI plus a CLI; every TUI action is also a CLI command).

Read first: CLAUDE.md (or AGENTS.md) and docs/design.md. docs/design.md
is the source of truth; update it in the same PR as any change it
describes.

Hosts
- Fedora 45 on an ASUS Zephyrus G14 runs everything: 22 GiB RAM, 8 GiB
  swap, GTX 1650 4 GB (CDI: nvidia.com/gpu=all), SELinux enforcing.
  Ollama runs as a Quadlet user service on 127.0.0.1:11434.
- The MacBook is a client only: `ssh -t fedora lab` for the TUI,
  `ssh -N fedora-lab` for the browser tunnel (8080/8443).

Hard rules
1. Container runtime is rootless Podman, always. Never install or call
   Docker. kind runs with KIND_EXPERIMENTAL_PROVIDER=podman.
2. Keep everything generic: no company names, code or data. The repo is
   public under the MIT licence.
3. Never commit secrets, AWS keys, kubeconfigs, Terraform state or
   binaries. Check `git status` before every commit.
4. Ask before anything destructive on the host: deleting files outside
   the repo, sudo, removing Podman volumes, editing ~/.aws or ~/.ssh.
5. Cloud: ap-south-1 only. Every AWS resource is tagged lab=true,
   owner=chanupen, expires-at=<RFC3339>. Ask before every cloud `lab up`,
   `tofu apply` or `destroy`. Follow prompts/13-cloud-session.md.
6. Only versions in standard support. Pin every version (charts, images
   by digest, tools with checksums). Check the compatibility table in
   internal/compat before changing a Kubernetes version or a pin.

Workflow
- One task, one branch, one worktree of your own:
  git worktree add ../devops-lab-wt-<task> -b <branch> origin/main
  Never work in ~/devops-lab (the owner's checkout) and never share a
  worktree with another session.
- Small conventional commits (feat:, fix:, docs:, chore:, test:). Each
  commit passes `go vet` and `go test` on its own.
- `make lint test` green before you open the PR. Table-driven tests.
- Open the PR, don't merge it. The PR description carries the evidence:
  test output, timings, memory added, what you ran on the host.
- Tests that touch the owner's main cluster stay inside the namespaces
  the task names. Never run `lab down` or `lab up` on the main lab unless
  the prompt says so. If you leave a fault or a stopped container, you
  restore it before anything else.
- When you are unsure what the owner wants, stop and ask with the
  options. Don't guess on anything that costs money or is hard to undo.

Code
- Go standard layout, golangci-lint, no global state outside cmd/.
- Libraries over CLIs: Helm SDK, kind library, terraform-exec, AWS SDK
  v2, client-go informers (never poll kubectl).
- TUI: first frame from cached state in under 100 ms; never block the
  key loop; work runs as tea.Cmds that stream messages.
- Mode differences live only in gitops/overlays/ and internal/provider/.
  If one mode needs a change anywhere else, explain why in the PR.

Writing
- Plain, professional, short sentences. No marketing tone, no emoji.
```
