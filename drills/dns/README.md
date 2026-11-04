# Service discovery fails across the app

The pager goes off: the sample app's frontend at https://podinfo.localtest.me:8443 cannot reach its backend, and other apps report errors too. Every pod you look at is Running. Find what broke, put it back, explain why it went down, and run `lab drills verify`.

## What happened

CoreDNS, the cluster's DNS server in `kube-system`, was scaled to zero. Pods find each other by Service name (`podinfo-backend.podinfo`), and every lookup goes to the `kube-dns` Service, which now has no endpoints. Nothing crashes: calls by name fail with `lookup podinfo-backend.podinfo: i/o timeout` or `server misbehaving`, while calls by IP still work.

The cause was a cost saver: kube-downscaler, run without `kube-system` on its exclude list, scaled every Deployment to zero outside "office hours". It recorded the replicas it found in the annotation `downscaler/original-replicas` and set `spec.replicas: 0` as its own field manager.

lab keeps working through all of it: it talks to the API server, which does not use cluster DNS. Argo CD does not: it cannot resolve the Git host, so its apps show comparison errors until DNS is back.

## Finding it

- The frontend's logs and `/api/echo` fail on the backend's name, while both pods run.
- `kubectl -n kube-system get deploy coredns` shows `0/0`; `kubectl -n kube-system get endpoints kube-dns` shows no addresses.
- Zero replicas is not a crash: someone set it. `kubectl -n kube-system get deploy coredns -o yaml --show-managed-fields` names the field manager that set `spec.replicas` (`kube-downscaler`) and when; the annotation holds the replicas before.
- The alert: `CoreDNSDown`. kube-prometheus-stack scrapes CoreDNS but has no alert for it being gone, so this drill adds one to the obs stack (`lab-drills` rules): no CoreDNS target up for a minute.

## The fix

```bash
kubectl -n kube-system scale deployment coredns --replicas=2
```

That restores service. The explanation is half the drill: CoreDNS went down because a cluster-wide automation (the downscaler) treated `kube-system` like any other namespace. The lasting fixes are to exclude system namespaces from it, to alert on CoreDNS being gone (this drill's alert), and a PodDisruptionBudget does not help here: it limits evictions, not a scale to zero.

When the drill ends, lab puts CoreDNS's Deployment back exactly as it was before (replicas and annotations).
