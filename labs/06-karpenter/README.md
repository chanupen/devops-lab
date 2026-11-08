# Lab 6: Karpenter, disruption budgets and spot replacement

Observe real scheduling, consolidation and replacement on an approved cloud lab.

## Goal

Explain scaling decisions and the trade-off between availability and blocked
disruption. Allow 50 minutes; advanced level.

## Prerequisites

Cloud only: an approved EKS session in `ap-south-1`, healthy `cluster`, Karpenter
and its interruption queue. Tools: rootless Podman, kubectl and jq.
Start lab 6 with `--mode cloud` using [the lifecycle guide](../../docs/guides/labs.md).
No other active lab/drill. Select private cloud exports; never log their credentials.

> **Billed — approval required:** the tasks launch and terminate real EC2
> nodes with disks. Prices vary; do not treat historical estimates as a cap.
> Keep NodePool `lab06`'s `limits.cpu: "6"`, arm64 spot/2-vCPU restrictions,
> taint and lab/owner/expiry tags. This limits this pool, not account spending.
> TTL/reaper safeguards still apply. Sending an interruption message causes
> real draining/replacement; it is not a harmless read-only probe.

Define the pinned AWS CLI wrapper after selecting the cloud environment:

```sh
aws() { podman run --rm --network host -e 'AWS_*' docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108 "$@"; }
```

**What you will see:** no output until invoked. Calls use the approved session,
not the local emulator. Review account/region before sending any queue message.

## Steps

1. [Read/create the pool](#step-1-read-the-nodepool-then-add-the-labs-own).
2. [Scale up](#step-2-scale-up-and-watch-karpenter-add-spot-nodes).
3. [Consolidate](#step-3-scale-down-and-watch-consolidation).
4. [Block disruption](#step-4-add-a-poddisruptionbudget-and-see-consolidation-stop).
5. [Allow one disruption](#step-5-let-the-pdb-allow-one-at-a-time).
6. [Test interruption response](#step-6-simulate-a-spot-interruption).

## What you should see

Lab pods use only their isolated NodePool. Pending pods trigger provisioning;
scale-down permits consolidation until a PDB blocks it. Replacement can leave
temporarily fewer available replicas. Do not depend on fixed node-launch timing.
The pool is capped at six vCPUs, normally at most three of its two-vCPU nodes.

## Hints

```sh
lab labs hint --mode cloud
```

**What you will see:** the next hint for the current step; no nodes are launched
by requesting a hint.

## How to verify

**Cloud reads; existing resources still bill:**

```sh
lab labs verify --mode cloud
```

**What you will see:** the step check and progress; each check also requires the
CPU cap to remain. **Reference solutions (spoilers):** [solution scripts](solution/).

## Cleanup

**Destructive/billed AWS teardown — get approval:**

```sh
lab labs reset --mode cloud
```

**What you will see:** `lab06-karpenter` and NodePool `lab06` removed.
Cleanup waits for its NodeClaims to disappear; resolve any failure before leaving.

```sh
lab labs stop --mode cloud
```

**What you will see:** the session ends. The main EKS environment still exists.
Complete [cloud down and leftovers checks](../../docs/getting-started/cloud.md#exact-session-sequence)
to end the approved session; preserve state on failure.

## Step 1: Read the NodePool, then add the lab's own

Read main NodePool `spot`, then apply provided `app/namespace.yaml` and `app/nodepool.yaml` unchanged. In ConfigMap `answers` in `lab06-karpenter`, record `spot`'s settings as keys `cpu-limit` and `consolidate-after`.

### Hints
- Compare requirements, limits, disruption and taints.
- A ready NodePool does not launch nodes without scheduling demand.

### Expected result
NodePool `lab06` is Ready with its cap/taint/label; answers match `spot`.

## Step 2: Scale up and watch Karpenter add spot nodes

Apply `app/inflate.yaml` and scale Deployment `inflate` from zero to four. Its pods request 700m CPU and must use only the lab pool.

### Hints
- Follow NodeClaims, node readiness, placement and Karpenter events.
- Allocatable CPU and DaemonSet requests differ from an instance's headline vCPUs.

### Expected result
Four available pods on at least two lab06 arm64 spot nodes within the CPU cap.

## Step 3: Scale down and watch consolidation

Scale inflate to two pods. Observe the consolidation policy and wait interval.

### Hints
- An underused node can be removable even when it is not empty.
- Distinguish deleting a node from replacing spot with cheaper spot.

### Expected result
One lab06 node remains with both available pods; the other is terminated.

## Step 4: Add a PodDisruptionBudget and see consolidation stop

Scale back to four, add a PDB selecting `app=inflate` with `maxUnavailable: 0`, then scale to two while retaining one pod per node.

### Hints
- ReplicaSet deletion preference can help retain the intended distribution.
- Scaling is not eviction; a PDB blocks drains rather than replica-count changes.
- Inspect both allowed disruptions and Karpenter's blocked-disruption events.

### Expected result
Two nodes retain one pod each; the zero-disruption PDB prevents consolidation.

## Step 5: Let the PDB allow one at a time

Change the PDB to `maxUnavailable: 1` without removing it. Watch consolidation.

### Hints
- Reconcile availability with a finite allowance for voluntary disruption.
- Wait for eviction, rescheduling and node termination before checking.

### Expected result
The PDB allows disruption and one lab06 node hosts both available pods.

## Step 6: Simulate a spot interruption

After approval, send an EC2 Spot Instance Interruption Warning event to Karpenter's configured SQS queue for a node belonging to `lab06`. Observe draining, termination and replacement.

### Hints
- Derive the instance identity from the selected lab node's providerID.
- Inspect the controller's queue setting and the EventBridge event schema.
- This tests the consumer response, not actual EC2 reclamation or the complete EventBridge routing path. Do not add an FIS experiment for this lab.

### Expected result
A lab06 NodeClaim records `SpotInterrupted` and disappears with the interrupted node. Two inflate pods become available on a live replacement lab06 node.
