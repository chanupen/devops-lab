# Lab 3: SLO and burn-rate alerts

Build alerts around user impact and compare handwritten rules with Sloth.

## Goal

Define a request-success SLI, a 99.5% availability objective over 30 days and
multiwindow burn alerts. Allow 45 minutes; intermediate level.

## Prerequisites

Local or cloud main lab, healthy `obs` and `sample-app`; kubectl, jq, curl and
rootless Podman. Start lab 3 using [the lifecycle guide](../../docs/guides/labs.md).
Work only in `lab03-slo` and `lab03-load`. No other active lab/drill.
Append `--mode cloud` to every labs hint/verify/reset/stop command below for
a cloud session; the examples otherwise use the configured mode.

**Danger:** step 5 deliberately sends errors to the sample app. Monitoring
records them beyond cleanup. **Billed — approval required in cloud:** traffic,
rules and workloads use EKS.

For the UIs, use two separate terminals and keep the port-forwards in foreground:

```sh
kubectl -n monitoring port-forward --address 127.0.0.1 svc/kps-prometheus 19090:9090
```

**What you will see:** loopback forwarding; open `http://127.0.0.1:19090`.

```sh
kubectl -n monitoring port-forward --address 127.0.0.1 svc/kps-alertmanager 19093:9093
```

**What you will see:** loopback forwarding; open `http://127.0.0.1:19093`.
A remote client needs its own SSH forwards. Do not bind either service to the LAN.

## Steps

1. [Choose an SLI and traffic](#step-1-pick-the-sli-and-send-traffic).
2. [Record error ratios](#step-2-write-recording-rules-for-the-sli).
3. [Set the objective](#step-3-set-a-995-slo-over-30-days).
4. [Write alerts](#step-4-write-multiwindow-multi-burn-rate-alerts).
5. [Send errors](#step-5-inject-errors).
6. [Observe and silence a page](#step-6-watch-the-fast-burn-alert-fire-and-acknowledge-it).
7. [Compare Sloth](#step-7-generate-the-same-rules-with-sloth-and-compare).

## What you should see

Recording rules are loaded, error/burn series rise during failed traffic and
alerts reach Alertmanager. Lab windows are shortened, not production defaults:

| Alert | Threshold | Lab windows | Production windows | Severity |
| --- | --- | --- | --- | --- |
| Fast | 14.4x | 10m and 2m | 1h and 5m | page |
| Slow | 6x | 1h and 10m | 6h and 30m | page |
| Budget | 1x | 12h and 1h | 3d and 6h | ticket |

Quiet services and earlier lab traffic can make long-window alerts fire early.
Do not use these shortened windows in production.

## Hints

```sh
lab labs hint
```

**What you will see:** the next hint for the current step.

## How to verify

```sh
lab labs verify
```

**What you will see:** a read-only step check; pass advances progress.
**Reference solutions (spoilers):** [solution scripts](solution/).

## Cleanup

**Destructive to exercise rules/traffic; cloud changes need approval:**

```sh
lab labs reset
```

**What you will see:** `lab03-slo` and `lab03-load` deleted and silences matching
`LabSLO*` expired. Recorded metrics are not erased.

```sh
lab labs stop
```

**What you will see:** the session ends. Use `--mode cloud` for a cloud session.
Stop both manual port-forwards with Ctrl-C; cloud EKS still bills until teardown.

## Step 1: Pick the SLI and send traffic

Find the frontend request metric and labels identifying server errors. Run Deployment `load` in `lab03-load`, sending about five requests/second to `http://podinfo-frontend.podinfo:9898/`.

### Hints
- Explore `http_requests_total` for namespace `podinfo` and frontend job labels.
- Decide which HTTP statuses count as service failure rather than client error.
- Use a digest-pinned curl image for repeatable traffic.

### Expected result
Frontend request rate exceeds one/second, with no server errors initially.

## Step 2: Write recording rules for the SLI

Create a PrometheusRule in `lab03-slo` recording error ratios over 2m, 10m, 1h and 12h. Name the series `lab03:sli_errors:ratio_rate2m`, ending similarly in `10m`, `1h` and `12h`.

### Hints
- Recording rules give later alerts one consistent SLI.
- Handle the absence of an error series without losing the successful-traffic ratio.
- Prometheus loads rules across namespaces; allow time for reconciliation.

### Expected result
All four series are loaded; the two-minute error ratio is zero.

## Step 3: Set a 99.5% SLO over 30 days

Record `lab03:slo:objective_ratio` as 0.995 with label `slo_window: 30d`. Record burn rates `lab03:slo:burn_rate2m`, `10m`, `1h` and `12h`.

### Hints
- Burn rate compares current errors with the objective's allowed error budget.
- Thirty days defines the budget period, not this Prometheus instance's retention.

### Expected result
The objective is 0.995 and all burn rates are zero before errors.

## Step 4: Write multiwindow, multi-burn-rate alerts

Create `LabSLOFastBurn`, `LabSLOSlowBurn` and `LabSLOBudgetTicket` using both windows and the thresholds/severities in the table.

### Hints
- The long window establishes significance; the short window checks ongoing impact.
- A page and a ticket have different urgency; keep their severity labels distinct.

### Expected result
All three alerts are loaded. Pages are inactive before errors; the budget ticket can reflect errors from an earlier run.

## Step 5: Inject errors

Run Job `errors` in `lab03-load`: about five requests/second to `http://podinfo-frontend.podinfo:9898/status/500` for ten minutes. Keep the successful traffic running and observe the ratios.

### Hints
- The Job should stop rather than restarting or retrying forever.
- Longer windows include earlier successful traffic and can rise more slowly.

### Expected result
The two-minute error ratio exceeds 0.072 and burn rates rise.

## Step 6: Watch the fast-burn alert fire and acknowledge it

Observe `LabSLOFastBurn` firing in Prometheus and Alertmanager. Create a one-hour silence for that alert, with a comment explaining ownership.

### Hints
- Alertmanager silence matching is distinct from fixing an incident.
- Inspect the silence state and matchers, not just whether notifications stopped.

### Expected result
Alertmanager has the page with severity `page` and an active matching silence. The slow page and budget ticket can follow as their windows accumulate errors.

## Step 7: Generate the same rules with Sloth and compare

Write a Sloth PrometheusServiceLevel for service `lab03-podinfo`, SLO `availability`, objective 99.5 and the same error/total queries. Generate with Sloth v0.16.0 and apply the result in `lab03-slo`.

### Hints
- Use image `ghcr.io/slok/sloth:v0.16.0@sha256:f0f0075b0d45c1cf684e92947508cc1d5bf573925f785f803a439b2306c8b9a5` through rootless Podman with only your workspace mounted.
- Sloth's query template takes a window placeholder and generates several windows.
- Compare the generated production windows and metadata with your shorter lab rules.

### Expected result
Prometheus loads Sloth series including `slo:sli_error:ratio_rate5m` with `sloth_service="lab03-podinfo"` and its page alert. Explain the different windows and Sloth's additional ticket tier.
