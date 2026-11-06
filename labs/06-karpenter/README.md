# Lab 6: Karpenter consolidation, PDBs and spot interruptions

Karpenter adds nodes when pods cannot be scheduled and removes them when they are no longer needed. That saves money, but it moves pods around: every node it removes is drained first. In this lab you watch Karpenter launch spot nodes for a workload, consolidate them when the workload shrinks, get blocked by a PodDisruptionBudget (PDB), and replace a node after a spot interruption warning.

The lab runs only in cloud mode, on the EKS cluster: `lab labs start 6 --mode cloud`. It needs the `cluster` stack. It works in the namespace `lab06-karpenter` and in its own NodePool, `lab06`, so it never moves the cluster's other pods. `lab labs reset` deletes both, and Karpenter then terminates the lab's nodes.

You need `podman`, `kubectl` and `jq`. The aws CLI runs as a pinned image through rootless Podman, and `lab labs env` gives it the cloud session's temporary keys:

```bash
eval "$(lab labs env --mode cloud)"
aws() { podman run --rm --network host -e 'AWS_*' docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108 "$@"; }
```

Check each step with `lab labs verify`. `lab labs hint` gives one hint at a time. `lab labs solution N` shows a reference answer. Two terminals help: one for commands, one watching `kubectl get nodeclaims,nodes -l karpenter.sh/nodepool=lab06 -w`.

## What it costs, and the cap

The lab's nodes are 2-vCPU arm64 spot instances, about $0.01 an hour each in ap-south-1, plus a 20 GB gp3 disk each. The lab uses at most three at a time, for well under $0.05 in all.

The NodePool has a hard cap: `limits.cpu: "6"`. Karpenter launches nothing that would take the pool past 6 vCPUs, so whatever you scale to, the lab cannot hold more than three of its nodes. Pods that do not fit stay Pending. Every verify script checks that the cap is still there. The cluster's own spot NodePool has its own cap, and the cloud lab's TTL and the TTL reaper still apply to every node.

## How the spot interruption is simulated

When EC2 needs a spot instance back, it sends an interruption warning two minutes ahead, as an EventBridge event. The cluster stack routes those events to an SQS queue, and Karpenter reads the queue: it taints the node, starts a replacement and drains the pods. Step 6 puts that same event on the queue yourself.

| Method | What happens | Cost |
|---|---|---|
| A test message on Karpenter's SQS queue (this lab) | Karpenter handles it exactly as a real warning: taint, drain, replace, terminate. EC2 does not reclaim the instance; Karpenter terminates it itself. | Nothing: one SQS request, inside the free tier. No new IAM role or AWS resource. |
| AWS FIS, `aws:ec2:send-spot-instance-interruptions` | EC2 sends a real warning, then interrupts the instance after two minutes. It also tests the EventBridge rule and the queue policy. | $0.10 per action-minute, about $0.20 a run, plus an IAM role and an experiment template that the lab would have to create, tag and clean up. |

The message tests what this lab is about, Karpenter's response and your workload's, for nothing. FIS is the better test of the AWS wiring (EventBridge to SQS), which the cluster stack sets up and `lab doctor --mode cloud` does not exercise; it is worth a run before you trust a real cluster's interruption handling.

## Step 1: Read the NodePool, then add the lab's own

A NodePool tells Karpenter which nodes it may launch (`requirements`), how much in all (`limits`), and when it may remove them (`disruption`). Read the cluster's NodePool, `spot`.

Then apply the lab's namespace and NodePool, `labs/06-karpenter/app/namespace.yaml` and `app/nodepool.yaml`, unchanged. Compare the two NodePools. Record two of `spot`'s settings in a ConfigMap `answers` in `lab06-karpenter`: `cpu-limit`, its CPU limit, and `consolidate-after`, how long a node must stay underused before Karpenter consolidates it.

### Hints
- `kubectl get nodepools`, then `kubectl get nodepool spot -o yaml`. `kubectl explain nodepool.spec.disruption` describes each field.
- `limits.cpu` counts the vCPUs of every node the pool holds. `consolidateAfter` is in `spec.disruption`.
- `kubectl -n lab06-karpenter create configmap answers --from-literal=cpu-limit=<value> --from-literal=consolidate-after=<value>`.
- Compare the requirements: both pools take arm64 spot instances only. The lab's pool takes 2-vCPU sizes only, carries a taint so that only the lab's pods land on it, and has a lower cap.

### Expected result
`kubectl get nodepool lab06` shows it Ready, with no nodes yet. Karpenter launches nothing until a pod needs it.

## Step 2: Scale up and watch Karpenter add spot nodes

Apply `app/inflate.yaml`. It is a Deployment of pause containers that only take room: each pod requests 700m CPU, and they run only on the lab's nodes. It starts at 0 replicas. Scale it to 4 and watch.

### Hints
- `kubectl -n lab06-karpenter scale deployment inflate --replicas=4`.
- Pending pods are Karpenter's signal. `kubectl get nodeclaims -w` shows a NodeClaim per node it decides to launch, before the node exists.
- A 2-vCPU node has about 1.9 vCPU allocatable, and DaemonSets (VPC CNI, kube-proxy, node exporter) take some of it. Two 700m pods fit; a third does not. So 4 pods need 2 nodes.
- `kubectl get nodes -l karpenter.sh/nodepool=lab06 -L node.kubernetes.io/instance-type,karpenter.sh/capacity-type,topology.kubernetes.io/zone` shows what Karpenter chose. Karpenter lists every type that fits and the requirements allow (16 here), and EC2 Fleet chooses with the `price-capacity-optimized` strategy: the pools least likely to be interrupted, then the cheapest of those. So it may not pick the cheapest type: in the test run it chose m8gd.large ($0.035/h) over t4g.medium ($0.011/h).
- `kubectl -n kube-system logs deploy/karpenter | grep -i -e launched -e created` shows its decisions.

### Expected result
Two spot nodes of `lab06` in under a minute (38 s in the test run), each with two inflate pods.

## Step 3: Scale down and watch consolidation

Scale inflate to 2. The two remaining pods fit on one node. Watch what Karpenter does, and how long it waits.

### Hints
- The pool's `consolidationPolicy` is `WhenEmptyOrUnderutilized`: Karpenter removes an empty node, and also a node whose pods fit elsewhere.
- `consolidateAfter: 30s` is how long a node must stay unchanged before Karpenter considers it.
- `kubectl get events -A --field-selector source=karpenter -w`, or `kubectl describe nodeclaim <name>`, shows why a node goes (`Empty`, `Underutilized`).
- The ReplicaSet chose which pods to delete; usually the newest. Which node became empty?
- You may also see `Unconsolidatable`: "SpotToSpotConsolidation is disabled, can't replace a spot node with a spot node". Karpenter can delete a spot node and move its pods, but by default it does not replace one spot node with a cheaper spot node (a feature gate turns that on).

### Expected result
One node of `lab06` left, running both pods. The other was drained and terminated.

## Step 4: Add a PodDisruptionBudget and see consolidation stop

A PDB limits voluntary disruptions: evictions for a drain, which is how Karpenter removes a node. Scale inflate back to 4 (two nodes again). Add a PDB for `app=inflate` that allows no disruption at all (`maxUnavailable: 0`). Then scale to 2 so that one pod stays on each node. Karpenter now has two half-empty nodes it would merge, and cannot.

### Hints
- When a ReplicaSet scales down, it deletes pods with a lower `controller.kubernetes.io/pod-deletion-cost` first. Annotate one pod on each node with a higher cost before you scale down: `kubectl -n lab06-karpenter annotate pod <pod> controller.kubernetes.io/pod-deletion-cost=1000`.
- `kubectl -n lab06-karpenter get pdb` shows `ALLOWED DISRUPTIONS`. With `maxUnavailable: 0` it stays 0.
- Scaling down is not an eviction: a PDB never stops `kubectl scale`. It stops drains.
- After a minute or two, `kubectl describe nodeclaim <name>` lists a `DisruptionBlocked` event: `Pdb prevents pod evictions (PodDisruptionBudget=[lab06-karpenter/inflate])`. The NodeClaims' `Consolidatable` condition is True: Karpenter would consolidate them.

### Expected result
Two nodes with one pod each, and they stay. A PDB that never allows a disruption also blocks node upgrades, `expireAfter` and drift replacement: the node is stuck until someone changes the PDB.

## Step 5: Let the PDB allow one at a time

Change the PDB to `maxUnavailable: 1`. Keep it: a PDB is useful when it allows some disruption.

### Hints
- `kubectl -n lab06-karpenter patch pdb inflate --type merge -p '{"spec":{"maxUnavailable":1}}'`.
- Karpenter evicts one pod, the PDB lets it, the pod is rescheduled on the other node, and the empty node goes.
- With more replicas, `maxUnavailable: 1` makes a drain move one pod at a time. A PDB that allows 1 is a common default for a service with a few replicas.

### Expected result
One node of `lab06`, running both pods, within a minute or two.

## Step 6: Simulate a spot interruption

Send Karpenter the interruption warning that EC2 would send for the node inflate runs on (see "How the spot interruption is simulated" above). Watch the node get tainted and drained, the pods move, and a replacement node arrive.

### Hints
- The instance ID is the end of the node's `spec.providerID`: `kubectl get node <name> -o jsonpath='{.spec.providerID}'`.
- The queue's name is in Karpenter's settings: `kubectl -n kube-system get deployment karpenter -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="INTERRUPTION_QUEUE")].value}'`. Its URL: `aws sqs get-queue-url --queue-name <name>`.
- The message is the EventBridge event, as JSON: `{"version":"0","id":"<uuid>","detail-type":"EC2 Spot Instance Interruption Warning","source":"aws.ec2","account":"<account>","time":"<now, RFC3339>","region":"ap-south-1","resources":["arn:aws:ec2:ap-south-1:<account>:instance/<id>"],"detail":{"instance-id":"<id>","instance-action":"terminate"}}`. Build it with `jq -n`, then `aws sqs send-message --queue-url <url> --message-body "$body"`.
- `kubectl get events -A -w | grep -i interrupt` shows Karpenter taking it: `SpotInterrupted` ("Spot interruption warning was triggered") and `TerminatingOnInterruption`, on the NodeClaim and the Node, within a second.
- A real warning gives two minutes. Here nothing forces that, but Karpenter acts at once either way. Ask what your workload needs to survive two minutes' notice: more than one replica, a PDB that allows a disruption, and a short shutdown.

### Expected result
Karpenter records the interruption on the NodeClaim, drains the node, terminates the instance and launches another spot node. inflate is back to 2 available pods on the new node, about 40 seconds later. Until then it runs on one pod: the drain does not wait for the replacement, which is why one replica is never enough on spot. That is the end of the lab: `lab labs reset` removes the NodePool and its nodes.
