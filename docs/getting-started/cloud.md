# Run an approved cloud session

[Docs index](../README.md) · [Architecture](../concepts/architecture.md) ·
[Local guide](local.md)

Cloud mode runs on AWS in `ap-south-1`. It is not needed for ordinary local
practice. This guide describes the commands; no cloud operation is performed
to maintain these docs.

> **Billed — approval required:** AWS charges continue until resources are
> deleted. Review the account, stacks, revision, duration, current prices and
> cleanup plan with the operator before starting. Get explicit approval before
> baseline apply, lab up, lab down or extending a session. Do not use `--yes`.

## Prerequisites

Have `lab` installed and this checkout available; use the
[source installation steps](local.md#get-the-source-and-install).
Keep a read-only GitOps deploy key configured as in the local guide.
Leave the first session on `minimal` plus `obs`, with Argo CD.

Examples assume a Linux operator host. You also need `zip` for the reaper
build, OpenTofu 1.10+, AWS CLI v2, an AWS account approved for this lab,
and an MFA-enabled IAM source user. The one-time baseline creates a
`lab-admin` role with AdministratorAccess. Do not bootstrap it in an unrelated
or production account.

Set up credentials for the source profile `devops` using your approved AWS
credential process. Keep credentials outside Git. In `~/.aws/config`, the
source profile can contain:

```ini
[profile devops]
region = ap-south-1
output = json
```

Do not give this source profile a `source_profile` line: it is the source,
not an assumed role. The IAM user's credentials belong in the AWS credential
store, not `lab.yaml`. Review `cloud.owner: chanupen` and `cloud.ttl: 2h` in
`lab.yaml`; the region must stay `ap-south-1`.

## Cost and TTL rules

| Rule | What it means |
| --- | --- |
| TTL | A new lab defaults to 2h; expiry cannot be more than 8h ahead, including extensions |
| Tags | Expiring resources carry `lab=true`, `owner=chanupen`, and RFC 3339 `expires-at` |
| Baseline | State bucket, IAM, budget and cleanup services are permanent; no `expires-at` |
| Reaper | Scheduled every 15 minutes; removes expired resources and starts server-side teardown |
| Apply preflight | Requires a non-dry-run reaper with a clean heartbeat/self-test within 30 minutes, supported EKS version and no running teardown |
| Budget | Default $30/month; alerts at 50/80/100%; the 100% action blocks new EC2 launches, not ongoing charges |
| Infrastructure | Spot nodes, no NAT gateway, no default load balancer, short log retention and gp3 storage |
| Cleanup | Down destroys the environment, sweeps residual resources and checks real leftovers |

Price the EKS control plane, EC2, EBS, ECR, S3 and cleanup services before
approval. Do not treat old example prices as a quote. TTL and budgets are not
a hard spending cap. Reaper/CodeBuild and storage can also cost money.
The baseline continues to exist and can bill after the lab is down.

The reaper is a fallback, not a reason to skip down. Failed teardown is
reported by email and retried; after capped failures, new cloud up is refused
until cleanup is resolved. Never bypass those safeguards.

## One-time baseline

Skip creation only if the approved account already has the correct baseline.
An ordinary session does not destroy it.

**Local build only; no AWS resources or charges are created by this command.**

```sh
make reaper
```

**What you will see:** compilation and packaging of `bin/reaper.zip`.
The baseline plan needs that package even when the baseline already exists.

Create the private input directory:

```sh
mkdir -p "$HOME/.lab/aws"
```

**What you will see:** no output; the directory exists outside the checkout.

Create `~/.lab/aws/baseline.tfvars` with your real alert address:

```hcl
alert_email = "alerts@example.net"
source_user = "devops"
reaper_dry_run = false
```

`source_user` is the IAM user name, not merely a CLI profile label.
Replace it if your approved source user differs.
The module defaults to dry run; explicitly disabling it arms deletion.
Review that change as part of baseline approval. Do not commit this file.

```sh
chmod 600 "$HOME/.lab/aws/baseline.tfvars"
```

**What you will see:** no output; only the file's owner can read/write it.

**Read-only AWS plan — session approval required; no infrastructure is applied.**

```sh
lab cloud baseline plan --profile devops
```

**What you will see:** the AWS account, identity, region and OpenTofu plan.
On first use, state is local and will move into the new S3 bucket after apply.
Review every change. On an existing baseline, unexpected replacements,
destruction or changes to the budget, schedule or SNS are not allowed as
part of an ordinary session. Stop and resolve that plan separately.

> **Billed and privileged — explicit approval required:** baseline apply creates
> permanent AWS services and IAM permissions. Check the printed account and
> region, and confirm the plan only after approval.

```sh
lab cloud baseline apply --profile devops
```

**What you will see:** the plan and an `Apply this plan to AWS account ... in
ap-south-1?` prompt. After an approved first apply, state moves into
`s3://lab-tofu-state-<account>/...`. Output includes `state_bucket`,
`lab_admin_role_arn`, `budget_name`, `reaper_function` and `reaper_dry_run`.
The last value must be false before a lab can start.

Subscribe/confirm the AWS budget and teardown email notifications sent to
your alert address. Keep them available during the session.

Configure the role profile in `~/.aws/config`, replacing both ARN placeholders:

```ini
[profile lab]
role_arn = arn:aws:iam::<account-id>:role/lab-admin
source_profile = devops
mfa_serial = arn:aws:iam::<account-id>:mfa/<device-name>
duration_seconds = 14400
region = ap-south-1
output = json
```

Use the role ARN from baseline output and your source user's actual MFA
device ARN. Do not copy a profile from another account.

## Exact session sequence

Approve the whole sequence and teardown before entering the billed window.
Run from the checkout in a terminal that can prompt for MFA.
If a local lab owns ports 8080/8443, free them through its approved teardown
or choose alternate tunnel ports; do not expose a listener to the LAN.
Quit any TUI that holds the shared mutation lock.

### 1. Log in

**AWS authentication — no infrastructure created; existing resources still bill.**

```sh
lab cloud login
```

**What you will see:** an MFA prompt, then `logged in to account ... as ...`.
Check that it is the approved account and `lab-admin` role.
The session lasts up to four hours and shares the AWS CLI role cache.
Allow at least 2.5 hours remaining for a two-hour session and cleanup.

### 2. Pass preflight

**Read-only AWS checks — existing resources still bill.**

```sh
lab doctor --mode cloud
```

**What you will see:** checks for OpenTofu, AWS CLI, role, state bucket, budget,
EKS version, TTL reaper and teardown build. Require all cloud checks to pass;
do not treat warnings or skipped checks as evidence of readiness.
After first baseline apply, wait for a scheduled reaper run and repeat this
check until its recent heartbeat and self-test pass.

### 3. Review infrastructure plans

For every session, rebuild the reaper package and repeat the baseline plan
from the previous section, even when the baseline already exists. Require no
unexpected changes, and stop for approval of any baseline change before
proceeding to the registry/cluster plan.

**Read-only AWS/OpenTofu plan — existing resources still bill.**

```sh
lab cloud plan
```

**What you will see:** ECR registry and VPC/EKS cluster plans. Nothing is
applied. This is different from `lab plan`, which lists stack order from state.
Get approval for the actual changes and cost before the next step.

### 4. Create the environment

> **Billed — explicit apply approval required:** this creates or changes ECR,
> EKS, nodes and their dependencies. They bill during bootstrap as well as use.
> A new environment gets a two-hour expiry; repeating up does not extend an
> unexpired environment.

```sh
lab up --mode cloud --ttl 2h
```

**What you will see:** preflight checks, a stack plan and an `Apply ... stacks
to AWS account ... in ap-south-1?` prompt that states the TTL and billing.
After confirmation, stack progress and app URLs appear. EKS bootstrap is not
instant. Check the logs if a stack fails.

**If this command fails, proceed to the approved down/leftovers steps below.
Do not debug a half-built environment on the billing clock.**

**Read-only status — the environment is still billed.**

```sh
lab status --mode cloud
```

**What you will see:** applied stacks, live app health, GitOps revision, URLs
and `expires-at`. Require healthy apps for a successful session; saved
`applied` state alone is insufficient.

### 5. Reach an application

**Access only — does not stop billing or change TTL.**

```sh
lab tunnel --mode cloud
```

**What you will see:** `forwarding 127.0.0.1:8080,8443 ...; Ctrl-C stops`,
followed by `https://<app>.localtest.me:8443` URLs. Keep this terminal open.
It forwards the Envoy Service, not a paid load balancer.

In another terminal, select the dedicated cloud kubeconfig:

```sh
export KUBECONFIG="$HOME/.lab/cloud/kubeconfig"
```

**What you will see:** no output. Subsequent kubectl requests target the cloud
lab, not the local cluster.

**Read-only Kubernetes certificate query — the cloud lab remains billed.**

```sh
kubectl -n envoy-gateway-system get secret lab-gateway-tls \
  -o jsonpath='{.data.tls\.crt}' | base64 --decode > "$HOME/.lab/cloud-gateway.crt"
```

**What you will see:** no output on success; a public PEM certificate is saved.
The Gateway certificate is self-signed. Never extract its private key.

**Application request — the cloud lab remains billed.**

```sh
curl --cacert "$HOME/.lab/cloud-gateway.crt" https://podinfo.localtest.me:8443
```

**What you will see:** a sample-app response through the loopback tunnel.
Use explicit certificate trust, not insecure TLS flags.
For a remote client, use the [local guide's SSH forwarding steps](local.md#remote-access)
in addition to the cloud tunnel, and trust the public certificate there.

### 6. Destroy and check leftovers

Stop the tunnel with Ctrl-C and quit the TUI first. Stopping either does not
destroy AWS resources or stop their charges.

> **Destructive — teardown approval required:** down deletes the environment
> and its data. Resources continue to bill until deletion completes.
> Cleanup approval should already be part of the session plan.

```sh
lab down --mode cloud
```

**What you will see:** a destroy plan and confirmation prompt. After approval,
cleanup releases external resources, destroys the cluster/registry, sweeps
residual ENIs, volumes and secrets, and lists real leftovers.
It does not wait for each application to become healthy.

**Read-only AWS inventory — leftovers can still bill.**

```sh
lab cloud leftovers
```

**What you will see on complete cleanup:**

```text
nothing tagged lab=true is left outside the baseline
```

Stale tagging-index entries appear separately and are ignored only after
direct resource checks. Failed lookups are not proof of cleanup. Real
leftovers or an unsuccessful down require follow-up with the operator; do
not delete state, disable the reaper or remove the baseline.

The cleanup gate is no real lab leftovers and no failed cleanup checks.
Also inspect the approved account directly in `ap-south-1`: unattached EBS
volumes and ENIs, lab-tagged secrets including pending deletion, VPCs, EKS
clusters, instances and ECR repositories. Use the AWS service inventories,
not only the eventually consistent tagging index. Separate permanent baseline
resources from session resources.

Record the UTC timeline, cleanup evidence and estimated session cost.
Cost Explorer can lag a day; record the actual figure when it arrives.
Baseline charges may remain.
Do not extend automatically; an approved extension is another billed change.
