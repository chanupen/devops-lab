# The database got slow

The pager goes off: `SampleDBLatencyFastBurn`. sample-db's latency SLO says 99% of `/api/check` requests answer within 250 ms, and it is burning its 30-day error budget more than 14 times too fast. Requests still succeed, only slowly. Nothing was deployed. Find out why the requests are slow, fix it, and run `lab drills verify`.

## What happened

Postgres's network interface got a `tc netem` rule that delays every packet it sends by 300 ms (±50 ms). It is the rule a network chaos experiment adds (Chaos Mesh's NetworkChaos, Litmus, or someone with `tc` and a debug container) and a failed cleanup leaves behind. Each `/api/check` waits for at least one round trip to Postgres, often two (pgxpool pings a connection idle for over a second before it hands it out), so requests take 300 to 700 ms instead of a few. Everything else looks healthy: no errors, no restarts, Postgres's CPU is idle and its queries are fast.

How lab injects it, and why this way: the kind node runs `tc` inside the Postgres pod's network namespace (`podman exec` on the node, `nsenter -n` into the container's process). On rootless Podman the node is root in its own user namespace and owns the pod's network namespace, so this needs no privileged pod, no `sudo` on the host and no extra image, and costs no memory. Chaos Mesh would do the same with a CRD and a daemon on each node, about 300 MiB more (its controller, dashboard and chaos-daemon DaemonSet); worth it once the lab has several network drills, not for one.

## Finding it

- The alert: `SampleDBLatencyFastBurn` (lab 3's pattern, on sample-db: the error ratio over the budget, over 10 minutes and 2 minutes). `SampleDBLatencySlowBurn` follows if it lasts.
- The latency: `histogram_quantile(0.99, sum by (le) (rate(sampledb_http_request_duration_seconds_bucket{path="/api/check"}[2m])))` in Prometheus or Grafana jumps from a few milliseconds to about half a second. The error ratio `sampledb:sli_latency:error_ratio_rate2m` goes to 1.
- Not the database itself: the same query inside the Postgres pod, over the local socket, takes well under a millisecond (`psql` with `\timing`). Postgres's CPU (`container_cpu_usage_seconds_total`) is idle.
- The network: `kubectl -n sample-db debug postgres-0 -it --profile=netadmin --target=postgres --image=docker.io/nicolaka/netshoot:v0.15@sha256:47b907d662d139d1e2f22bfe14f4efca1e3f1feed283572f47c970c780c03b61 -- tc qdisc show dev eth0` shows `qdisc netem ... delay 300ms 50ms`. A `ping` from the app's pod to Postgres shows the same delay.
- Traces would point at the database call straight away; the lab's obs stack has no Tempo yet, so this drill works from metrics.

## The fix

Remove the rule:

```bash
kubectl -n sample-db debug postgres-0 -it --profile=netadmin --target=postgres \
  --image=docker.io/nicolaka/netshoot:v0.15@sha256:47b907d662d139d1e2f22bfe14f4efca1e3f1feed283572f47c970c780c03b61 \
  -- tc qdisc del dev eth0 root
```

Deleting the pod (`kubectl -n sample-db delete pod postgres-0`) also clears it, since the new pod gets a new network namespace, at the cost of a Postgres restart. Then the lasting fixes:

- Chaos experiments with a duration and an owner, run through a tool that cleans up (Chaos Mesh, Litmus), never by hand.
- Who may add `NET_ADMIN` debug containers: `kubectl debug --profile=netadmin` needs the right to create ephemeral containers; limit it with RBAC (`pods/ephemeralcontainers`) and a policy on capabilities.
- An SLO on latency, not only on errors: this fault never returned an error.

## What lab puts back

Restore deletes any `netem` rule on every Postgres container of sample-db, checks none is left, and waits until `/api/check` is fast again.
