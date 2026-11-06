# 04 — Phase 3a: AWS account baseline

Needs: phase 2 merged; an AWS account; IAM user `devops` with access keys in `~/.aws/credentials` and an MFA device; OpenTofu ≥ 1.10. The first apply is the only one that uses `--profile devops`. Follow `13-cloud-session.md` for every apply.

```text
infra/terraform/aws/baseline, applied by `lab cloud baseline plan|apply`
(OpenTofu via terraform-exec). Permanent resources: tags lab=true,
owner, component=baseline, and NO expires-at (the reaper keeps anything
without one). Private inputs (alert email) in ~/.lab/aws/baseline.tfvars,
never in the repo.

1. State bucket lab-tofu-state-<account>: versioned, AES-256, public
   access blocked, TLS-only policy, old versions kept 90 days, native S3
   locking (use_lockfile), no DynamoDB. First apply uses local state;
   once the bucket exists lab writes the S3 backend and runs
   `tofu init -force-copy`. lab picks the backend by whether the bucket
   exists.
2. lab-admin role: assumable only by the devops user, only with MFA,
   at most 4 hours. Restrict devops with an inline policy so it can do
   nothing but assume lab-admin. `lab cloud login` asks for the MFA code
   once and shares the AWS CLI's role cache (~/.aws/cli/cache), so one
   code serves lab, tofu, `aws eks get-token` and the aws CLI. lab passes
   temporary keys to tofu. lab never edits ~/.aws; it prints the [profile
   lab] block for the owner to add (role_arn, source_profile = devops,
   mfa_serial, duration_seconds 14400). Without a terminal, login says it
   needs one and keeps the cached session.
3. Budget lab-monthly: $30/month, email alerts at 50/80/100% of actual
   spend. At 100% a budget action attaches a deny-ec2:RunInstances policy
   to lab-admin, through a role that may attach/detach only that policy.
4. TTL reaper lab-ttl-reaper: a Go Lambda (arm64, provided.al2023,
   `make reaper` builds a byte-identical zip) on a 15-minute EventBridge
   schedule.
   - Lists lab=true resources with the tagging API; deletes those whose
     expires-at has passed: node groups and instances first, then load
     balancers, EBS volumes, Elastic IPs, Secrets Manager secrets (no
     recovery window). Inside an expired lab VPC also every load
     balancer (Classic too), target group, unattached ENI and non-default
     security group, tagged or not.
   - The tagging API lags up to an hour: look each resource up with a
     direct Describe call; terminated/NotFound/closed spot requests and
     instant fleets are "gone", not leftovers.
   - For tofu-managed stacks, start the CodeBuild project lab-teardown,
     only if the stack's state in S3 holds resources and its teardown
     bundle's expires_at has passed; at most 6 attempts
     (teardown/state.json), then email "manual action needed". If it
     can't read the attempt count, start the build anyway and report.
   - Each run self-tests (reads teardown/, sees the lab-teardown project)
     and writes reaper/heartbeat.json, clean or not. A failed run emails
     through a Lambda on-failure destination (SNS lab-teardown).
   - Starts in dry-run: no delete permissions at all until
     reaper_dry_run = false in baseline.tfvars.
5. CodeBuild lab-teardown (arm1.small): runs tofu destroy on the
   cluster and registry state from bundles lab uploads before each apply
   (teardown/<stack>.zip with lab.auto.tfvars.json), sweeps again and
   emails a summary. It exits without acting if a bundle's expires_at is
   in the future or a lab apply took the state lock in the last hour.
   Its role reads broadly; every destructive action is limited to
   ap-south-1 and aws:ResourceTag/lab = true.
6. lab doctor --mode cloud: tofu present, the lab profile assumes
   lab-admin, bucket and budget exist, reaper out of dry run, heartbeat
   from the last 30 minutes and clean, no teardown build running.
7. Before every plan/apply: check that every AWS managed policy ARN in
   infra/ exists (aws iam get-policy), so a typo fails in seconds.

Test the dead man's switch for real (see 13-cloud-session.md): a tiny
tagged resource with a short TTL, no lab down, and prove the reaper and
the teardown build remove it. Repeat until it passes; put the timeline
in the PR.
```
