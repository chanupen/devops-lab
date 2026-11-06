# 14 — Lab 6 (Karpenter) and the phase 4 gate

Needs: 12 merged after a passing cloud session. Gate: labs pass in both modes.

```text
Lab 6 and the phase 4 gate.
Worktree: git worktree add ../devops-lab-wt-lab6 -b feat/lab-6-karpenter origin/main

1. Labs on cloud: feed the ECR address into lab labs as
   LAB_REGISTRY_PUSH/PULL in cloud mode; Lab 2 builds its app for arm64.
   Lab 4's aws commands run against real Secrets Manager (no endpoint).
2. Lab 6, cloud only (modes [cloud], needs: lab): Karpenter
   consolidation and spot interruption. Steps roughly:
   - read the NodePool, its requirements and disruption settings;
   - scale a workload up and watch Karpenter add spot nodes;
   - scale down and watch consolidation remove or replace nodes;
   - add a PodDisruptionBudget and see how it changes consolidation;
   - simulate a spot interruption and watch the node drain and get
     replaced. Pick the cheapest realistic method (a test message on
     Karpenter's interruption SQS queue, or AWS FIS) and explain the
     choice and its cost.
   A hard cap on nodes/CPU in the lab's NodePool so it can't run away.
   reset.sh removes the lab's NodePool and workloads.
3. Gate: labs 2–5 pass on cloud with their solutions (e2e), Lab 6 on
   cloud. Lab 1 stays local-only (kind scratch cluster): record that in
   docs/design.md as the one exception. Drills stay local-only for now;
   note which could run on cloud later.
4. Plan the cloud session to fit one 2h TTL (13-cloud-session.md).
   Before asking me: the plan, time and cost.

PR: per-lab cloud results, Lab 6 learner pass, session timeline,
leftovers, cost.
```
