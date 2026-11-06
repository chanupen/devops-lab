# 13 — Running a cloud session safely

Use this for every `lab up --mode cloud`, `lab cloud baseline apply` or `tofu destroy`. It costs real money, so the agent proposes and you approve.

## Before you start

- Run `lab cloud login` yourself in a terminal (MFA). It lasts 4 hours. Start a session only if at least 2.5 hours remain.
- Check that `[profile devops]` in `~/.aws/config` has no `source_profile` line. Tools that write AWS profiles (for example `claws`) have added one before, and that breaks every `lab` cloud command.
- `lab doctor --mode cloud` must be all green: the reaper is out of dry run, its heartbeat is recent and clean, and no teardown build is running.

## Prompt

```text
Cloud session for <PR / task>. Before you run anything, give me:
- the plan as numbered steps with the exact commands,
- the expected time and cost (EKS $0.10/h, the instances, IPv4,
  volumes), checked against the AWS pricing pages,
- the stacks and the revision.

Rules:
1. Baseline plan first. If it shows changes, show me the full plan and
   wait. Allowed without asking: nothing. A baseline change must not
   destroy or replace anything, or touch the budget, schedule or SNS.
   Rebuild the reaper zip (make reaper) before planning.
2. lab up --mode cloud --ttl 2h --revision <branch> --stacks <list>.
   On ANY failure: collect logs, then lab down --mode cloud at once.
   Don't debug on the clock.
3. Checks: every Argo CD app Synced and Healthy; podinfo and Grafana
   through lab tunnel; no pod stuck on an image without an arm64 build.
4. lab down --mode cloud. Then lab cloud leftovers (nothing outside the
   baseline; stale index entries are fine), and check directly:
   unattached EBS volumes, unattached ENIs, lab=true secrets (including
   pending deletion), VPCs, EKS clusters, instances, ECR.
5. Never extend past 2h without asking me. Never edit ~/.aws.
6. If you lose track (a new session, a crash), first check what exists
   in AWS (lab status --mode cloud, lab cloud leftovers) and tell me
   before doing anything else.

Put the timeline (UTC), the leftovers output and the cost in the PR.
Cost Explorer lags a day; add the real figure the next day.
```

## If something is left running

```bash
cd <worktree> && ./bin/lab status --mode cloud && ./bin/lab cloud leftovers
./bin/lab down --mode cloud
```

If `lab down` can't finish, the reaper starts the `lab-teardown` build within 15 minutes of `expires-at`, and the budget action blocks new instances at 100%. Check the reaper's log with `aws logs tail /aws/lambda/lab-ttl-reaper --profile lab --region ap-south-1 --since 1h`.
