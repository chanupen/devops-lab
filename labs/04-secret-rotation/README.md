# Lab 4: Rotate a database password without restarting the app

A database password should change regularly, and at once when it leaks. Restarting every app that uses it is slow and drops requests. In this lab you rotate the password of a running app while traffic flows. Secrets Manager holds the credentials, External Secrets Operator (ESO) copies them into a Kubernetes Secret, the kubelet updates the file the app reads, and the app moves to the new credentials on its own. A traffic Job counts every failed request; the goal is zero.

The lab runs on your main lab's cluster, in the namespaces `lab04-app` (Postgres and the app) and `lab04-traffic` (the traffic Job). It needs the `secrets` and `sample-db` stacks (`lab up`). It uses the sample-db stack's app image, but deploys its own copy, so the stack is not touched.

Locally, Secrets Manager is Moto, an AWS API emulator, on `127.0.0.1:4566`. The commands are the same as on AWS; only the endpoint differs. `lab labs` sets these variables for the verify scripts; set them in your shell too:

```bash
export AWS_ENDPOINT_URL_SECRETS_MANAGER=http://127.0.0.1:4566 AWS_REGION=ap-south-1 \
  AWS_ACCESS_KEY_ID=lab-dummy AWS_SECRET_ACCESS_KEY=lab-dummy-not-a-secret
```

The keys are dummies: Moto checks none. In cloud mode you leave all four unset and use your AWS profile.

You need `podman`, `kubectl` and `jq`. The aws CLI runs as a pinned image through rootless Podman, so there is nothing else to install:

```bash
aws() { podman run --rm --network host -e 'AWS_*' docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108 "$@"; }
```

Paste it into your shell after the exports. `--network host` reaches Moto on 127.0.0.1, and `-e 'AWS_*'` passes the variables into the container.

Check each step with `lab labs verify`. `lab labs hint` gives one hint at a time. `lab labs solution N` shows a reference answer.

## How the credentials reach the app

```text
Secrets Manager ──ESO, every refreshInterval──▶ Secret db-app ──kubelet, about a minute──▶ /etc/db/{username,password} ──app, every 2 s──▶ new connection pool
```

Postgres has three kinds of role:

| Role | Logs in | Used for |
|---|---|---|
| `postgres` | yes | the superuser; its own secret, never rotated in this lab |
| `app` | no | a group role that holds the app's privileges |
| `app_v1`, `app_v2`, ... | yes | the app's logins, members of `app` |

A rotation adds a new member of `app` and retires the old one. The privileges stay on `app`, so nothing else changes. While both members can log in, the old and the new password both work: those are the dual credentials that make a rotation safe.

## Step 1: Deploy the app and read its setup

Create two secrets in Secrets Manager, each a JSON object with `username` and `password`:

- `lab04/db/admin` for `postgres`
- `lab04/db/app` for `app_v1`

Use random passwords. Then deploy the manifests in `labs/04-secret-rotation/app` (all but `traffic.yaml`), with `SAMPLE_DB_IMAGE` replaced by the image the sample-db stack runs. When the app is ready, record its pod's UID on the Deployment as the annotation `lab.devops/pod-uid`. Step 5 uses it to prove the pod was never replaced.

While it starts, read the setup. How often does ESO check Secrets Manager? Where does the app find its credentials, and how are they mounted?

### Hints
- A password: `head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n'`. Build the JSON with jq, so quoting cannot break it: `aws secretsmanager create-secret --name lab04/db/app --secret-string "$(jq -cn --arg p "$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')" '{username: "app_v1", password: $p}')"`.
- The image: `kubectl -n sample-db get deployment sample-db -o jsonpath='{.spec.template.spec.containers[0].image}'`. It is pinned by digest. Then `sed "s|SAMPLE_DB_IMAGE|$img|" app.yaml | kubectl apply -f -`.
- `kubectl -n lab04-app get externalsecret` shows whether ESO could read both secrets. The `refreshInterval` is in its spec.
- The app's credentials are a Secret volume at `/etc/db`, with no `subPath`. Postgres gets its own from environment variables, which it reads only at its first start.
- The UID: `kubectl -n lab04-app get pods -l app=sample-db -o jsonpath='{.items[0].metadata.uid}'`, then `kubectl -n lab04-app annotate deployment sample-db lab.devops/pod-uid=<uid>`. Annotating the Deployment's own metadata does not restart anything; only a change to its pod template would.
- Ask the app which user it is: `kubectl get --raw /api/v1/namespaces/lab04-app/services/sample-db:http/proxy/api/check`.

### Expected result
Both ExternalSecrets are `Ready`, Postgres and the app are running, and `/api/check` answers `{"user":"app_v1"}`. ESO checks Secrets Manager every hour (`refreshInterval: 1h`).

## Step 2: Start traffic that counts failures

Start the traffic Job: `kubectl apply -f labs/04-secret-rotation/app/traffic.yaml`. It calls `/api/check` about 4 times a second. Each call runs a query as the app's database user. It logs every failed request, and every 10 seconds it logs how many succeeded and failed so far. Leave it running until the end of the lab; it stops on its own after 45 minutes.

### Hints
- `kubectl -n lab04-traffic logs -f job/traffic` follows its log. Keep it open in a second terminal.
- If it reports a failure before you rotated anything, the app is not healthy yet. Fix that, then delete the Job and apply it again, so the count starts at zero.

### Expected result
The Job runs and its log shows lines like `progress ok=240 failed=0`.

## Step 3: Add a second login role

In Postgres, create the role `app_v2` with a new random password, as a member of `app`. Do not change `app_v1`. Keep the new password for step 4.

### Hints
- Connect as the superuser over the pod's local socket: `kubectl -n lab04-app exec -it postgres-0 -- psql -U postgres -d app`.
- `CREATE ROLE app_v2 LOGIN PASSWORD '...' IN ROLE app;`. `IN ROLE app` makes it a member, so it has the group's privileges.
- `\du` lists the roles and their memberships.
- Keep the password in a file only you can read (`umask 077`), not in your shell history.

### Expected result
`\du` shows `app_v2` as a member of `app`. Both `app_v1` and `app_v2` can log in, and the app still answers as `app_v1`.

## Step 4: Update the secret in Secrets Manager

Store `app_v2`'s username and password as the new value of `lab04/db/app`. Update the secret; do not delete and re-create it.

### Hints
- `aws secretsmanager put-secret-value --secret-id lab04/db/app --secret-string "$(jq -cn --arg p "$(cat ~/lab04/app_v2.password)" '{username: "app_v2", password: $p}')"`.
- Secrets Manager keeps versions. The new one gets the label `AWSCURRENT` and the old one `AWSPREVIOUS`: `aws secretsmanager get-secret-value --secret-id lab04/db/app --version-stage AWSPREVIOUS`.
- Order matters. If you update the secret before the role exists, the app cannot connect with the new credentials. This app then keeps its old connection pool, but many apps would fail.

### Expected result
`get-secret-value` returns `app_v2`, the previous version still holds `app_v1`, and the app still answers as `app_v1`: nothing in the cluster has changed yet.

## Step 5: Watch the app switch without a restart

The app still uses `app_v1`, because ESO checks again only after its `refreshInterval`, an hour. Make ESO sync now. Then watch the Secret change, the kubelet update the file, and the app move to `app_v2`. Confirm that the pod is the same one as in step 1 and that it never restarted.

### Hints
- ESO syncs at once when an ExternalSecret's annotations change: `kubectl -n lab04-app annotate externalsecret db-app force-sync=$(date +%s) --overwrite`. Lowering `refreshInterval` works too, but it also makes ESO call Secrets Manager more often from then on.
- `kubectl -n lab04-app get secret db-app -o jsonpath='{.data.username}' | base64 -d` shows the Secret. The kubelet updates the mounted files on its next sync, usually within a minute.
- The app logs `moved to new credentials` when it switches: `kubectl -n lab04-app logs deployment/sample-db`.
- Compare `.metadata.uid` and `.status.containerStatuses[0].restartCount` of the app's pod with step 1.
- The app's metrics count the switch: `kubectl get --raw /api/v1/namespaces/lab04-app/services/sample-db:http/proxy/metrics | grep sampledb_credential_reloads_total`.

### Expected result
`/api/check` answers `{"user":"app_v2"}`. The pod's UID is the one you recorded, its `restartCount` is 0, and `sampledb_credential_reloads_total` is 1. The traffic Job still shows `failed=0`.

## Step 6: Retire the old role and prove it

Stop `app_v1` from logging in, and end any session it still has. Then prove that its old password is rejected, and that the traffic Job saw no failed request through the whole rotation.

### Hints
- `ALTER ROLE app_v1 NOLOGIN;` blocks new logins and keeps the role, so you can undo it with `LOGIN` if something still used it. `DROP ROLE` is final.
- `NOLOGIN` does not close open sessions. Look for them first: `SELECT pid, application_name FROM pg_stat_activity WHERE usename = 'app_v1';`. Then end them with `pg_terminate_backend(pid)`. The app closed its old pool when it switched, so there should be none.
- To test a password, connect from inside the Postgres pod to its own IP: `kubectl -n lab04-app exec -it postgres-0 -- sh -c 'psql -h "$(hostname -i)" -U app_v1 -d app'`. Do not use 127.0.0.1 or the local socket: this Postgres image trusts those and never checks a password.
- The old password is still in Secrets Manager, as the version `AWSPREVIOUS`.
- `kubectl -n lab04-traffic logs job/traffic | grep -c FAILED` counts failed requests.

### Expected result
Logging in as `app_v1` fails with `role "app_v1" is not permitted to log in`. The app answers as `app_v2`, and the traffic Job's latest line still says `failed=0`.

## Why a file, and why no subPath

Environment variables and `subPath` mounts can both carry a Secret, but neither can carry a rotation:

- **Environment variables** are copied into the process when the container starts. A changed Secret never reaches a running process. Only a new container sees it, so every rotation means a restart. Postgres in this lab reads its superuser from environment variables, and that is fine: it reads them once, at its first start.
- **subPath mounts** are bind mounts of one file, made when the container starts. The kubelet updates a Secret volume by writing a new directory and swapping a symlink (`..data`) to it. A subPath mount still points at the old file, so the container never sees the change.
- **A whole Secret volume, without subPath**, follows the symlink swap. The kubelet updates it within about a minute, and an app that re-reads its files, as this one does every 2 seconds, picks up the change without a restart.

The app does one more thing: it opens a pool with the new credentials and switches only once that works. If the new credentials fail, it keeps the old pool and logs why. That is why the order is: role first, then secret, then revoke.

`lab labs reset` deletes `lab04-app`, `lab04-traffic` and the two `lab04/` secrets in Secrets Manager. Your `~/lab04` directory stays.
