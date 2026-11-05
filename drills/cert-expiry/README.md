# A certificate that cannot renew

A warning fires: `CertificateExpiringSoon` for `sample-db/sample-db-tls`, the certificate sample-db serves HTTPS with. It is past its renewal time and was not renewed. Last week the team moved sample-db to one-hour certificates; today's releases look routine and nobody changed Git. Find out why renewal fails, fix it the way the team deploys (Argo CD), and run `lab drills verify`.

## What happened

Two releases, both by Helm parameters on the `sample-db` Application, the way a CD tool with Argo CD write-back releases (Git did not change):

1. A release moved the certificate to a one-hour lifetime (`tls.duration=1h`, `tls.renewBefore=58m`), the shortest cert-manager allows: renewed every two minutes. cert-manager issued one at once.
2. A "CA rotation" release set `tls.caSecretName=sample-db-ca-2026`: the CA Issuer `sample-db-ca` now signs with a Secret of that name. Nobody created it. The Issuer turns `Ready=False` ("secret not found").

Two minutes later the certificate reaches its renewal time. cert-manager creates a CertificateRequest, which stays pending: its issuer is not ready. The certificate stays `Ready` (the old one is still valid), so nothing looks broken until it expires, here within the hour, in real life at the end of a 90-day certificate's last 30 days, often on a weekend. When it expires, every HTTPS client of sample-db that checks certificates fails.

## Finding it

- The alert `CertificateExpiringSoon` (added by the drills to the obs stack): `certmanager_certificate_renewal_timestamp_seconds` is more than two minutes in the past and the certificate has not expired yet. cert-manager's metrics are scraped by the obs stack.
- `kubectl -n sample-db describe certificate sample-db-tls`: `Renewal Time` in the past, `Not After` within the hour, the condition `Issuing` with "Renewing certificate as renewal was scheduled".
- `kubectl -n sample-db get certificaterequest`: the newest one is not `Ready`; `describe` says "Referenced issuer does not have a Ready status condition".
- `kubectl -n sample-db describe issuer sample-db-ca`: `Ready False`, "Error getting keypair for CA issuer: secrets \"sample-db-ca-2026\" not found".
- `cmctl status certificate -n sample-db sample-db-tls` shows all of it in one view.
- `argocd app history sample-db`: two releases with Helm parameters; `argocd app get sample-db -o yaml` shows `tls.caSecretName`.

## The fix: undo the CA change through Argo CD

```bash
argocd app history sample-db
argocd app set sample-db --sync-policy none      # Argo CD refuses to roll back while auto-sync is on
argocd app rollback sample-db <id of the release before the CA change>
# or keep auto-sync and remove only the bad parameter:
argocd app unset sample-db -p tls.caSecretName
```

The Issuer is `Ready` again, the pending CertificateRequest is signed, and the certificate's renewal time moves into the future. sample-db reloads the renewed Secret without a restart.

Creating the missing Secret by hand (a copy of the CA) also makes the Issuer ready, but it puts a CA key in a Secret that nothing manages, outside Git and outside cert-manager. Fix the release, not the symptom.

Then the lasting fixes:

- Alert on renewal, not on expiry: `CertificateExpiringSoon` fires at the renewal time, with most of the lifetime left. An expiry alert at 7 days left fires too late to be calm.
- A CA rotation is a change of its own: issue the new CA first (a Certificate with `isCA`), check the Issuer is `Ready`, then switch.
- Argo CD health for cert-manager resources marks the app Degraded when an Issuer is not ready: watch app health, not only sync.

## What lab puts back

lab saved `lab-bootstrap` and `sample-db` before the fault. Restore puts them back, so Argo CD syncs Git's release (90-day certificates, the right CA Secret), waits until the Issuer and the certificate are Ready with a certificate of Git's lifetime, and deletes the CertificateRequests the drill made.
