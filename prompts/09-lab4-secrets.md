# 09 — Lab 4, secrets and sample-db stacks, Moto

Needs: 08 merged.

```text
Lab 4: rotate a database password without restarting the app.
Worktree: git worktree add ../devops-lab-wt-lab4 -b feat/lab-4-secrets origin/main

1. Moto as the local AWS emulator (not LocalStack: since 2026.03 its
   image needs an auth token, the free plan is account-bound and
   non-commercial, and the last tokenless image gets no patches).
   Re-check LocalStack's terms first; if anything changed, stop and tell
   me. motoserver/moto pinned by digest, container lab-moto on the kind
   network, published on 127.0.0.1:4566 only, dummy keys.
   - Moto keeps no state. lab up re-seeds it from the Kubernetes Secrets
     ESO keeps, so passwords still match Postgres. Skip ExternalSecrets
     with spec.target.template (their Secret isn't the remote value) and
     warn. Test: stop/start lab-moto, lab up, same value hashes.
   - lab doctor (while secrets is deployed): lab-moto runs, answers
     ListSecrets, holds secrets; empty Moto is a warning fixed by lab up.
2. secrets stack: External Secrets Operator via Argo CD, a
   ClusterSecretStore lab-secrets on the AWS provider with Moto's
   endpoint locally (Pod Identity on AWS later, overlay only). compat
   row: ESO supports only its latest release and each lists one or two
   Kubernetes minors, so a Kubernetes bump needs an ESO bump in the same
   PR; say so in the comment and design doc.
3. sample-db stack (its own stack, not part of sample-app): Postgres
   (pinned) with its own pg_hba.conf (scram-sha-256 on every TCP
   connection, trust only on the Unix socket), and a small Go app
   (apps/sample-db) that reads credentials from a mounted Secret file
   (no env vars, no subPath), reloads on change, switches pools only
   once the new one answers, and exposes /healthz, /readyz, metrics.
   Roles: postgres superuser with its own never-rotated secret; NOLOGIN
   group role app holding every privilege; the app logs in as app_v1.
   lab up builds the app image into Zot's lab/ namespace and pins it by
   digest (tag = hash of the sources; rebuild when they change).
4. Lab 4 (needs: lab, lab04-app and lab04-traffic): read the setup;
   start a traffic Job counting failures; create app_v2 in group app;
   put-secret-value; force-sync the ExternalSecret (say it's a lab
   shortcut); confirm the switch with no restart (same pod UID,
   restartCount 0); check pg_stat_activity (including sessions whose
   role no longer exists), terminate leftovers, DROP ROLE app_v1; prove
   the old password (AWSPREVIOUS) is rejected through the Service and
   the traffic Job saw zero failures. README: AWS's alternating-users
   strategy, AWSCURRENT/AWSPREVIOUS/AWSPENDING, choosing refreshInterval
   (rotation delay vs API cost), why env vars and subPath can't rotate.
   The aws CLI runs as a pinned image through Podman.

e2e inside lab04-* on the main cluster; `lab up --stacks secrets,sample-db`
once. Learner pass. Memory per stack and lab time in the PR.
```
