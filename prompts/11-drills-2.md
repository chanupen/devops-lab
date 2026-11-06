# 11 — Drills cert-expiry, node-loss, slow-db, secret-drift

Needs: 10 merged.

```text
Four more drills on the same framework and safety rules.
Worktree: git worktree add ../devops-lab-wt-drills2 -b feat/drills-2 origin/main

- cert-expiry: sample-db served over HTTPS from a cert-manager CA; a
  Certificate with a very short lifetime and a broken issuer, so
  renewal fails. Find: Certificate/Order status, the issuer error. Fix
  through the GitOps path. Alert: CertificateExpiringSoon (scrape
  cert-manager in obs, keep its namespace label).
- node-loss: `podman stop` on a lab-worker container. Find: NotReady
  node, rescheduling; how a PodDisruptionBudget and replica count
  change the impact. Restore starts every stopped node and waits for
  Ready. Alert: KubeNodeNotReady.
- slow-db: tc netem latency in Postgres's network namespace, set from
  the kind node (not an ephemeral container: that leaves terminated
  containers and a Kyverno audit event). sample-db gets a latency
  histogram, a traffic client, and an SLO with burn-rate alerts
  (SampleDBLatencyFastBurn). Restore deletes every netem rule.
- secret-drift: the secret deleted in Moto. Find: ExternalSecret sync
  error (scrape ESO metrics; alert ExternalSecretSyncFailed); the app
  keeps the last Secret until a restart. Fix: restore the secret
  properly. Restore leaves Moto and the Kubernetes Secret exactly as
  before and never changes the DB password.

Non-Kubernetes state a restore needs goes in DRILL_STATE. e2e for all
seven drills (about 27 minutes; -timeout 60m); afterwards prove no
stopped lab-* container, no netem rule anywhere, Moto and DB Secrets
unchanged, every app Synced and Healthy. Blind learner run of each new
drill. PR: per-drill alert/detect/recover, memory added, the e2e PASS
lines.
```
