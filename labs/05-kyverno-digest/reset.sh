#!/usr/bin/env bash
# Back to the start of lab 5: no namespace lab05-apps (its Pods, Deployment,
# PolicyException and PolicyReports go with it) and no cluster-scoped object
# with the label lab.devops/lab=05-kyverno-digest (the ClusterPolicy).
set -uo pipefail
kubectl delete namespace lab05-apps --ignore-not-found --wait --timeout=180s
kubectl delete clusterpolicies.kyverno.io -l lab.devops/lab=05-kyverno-digest --ignore-not-found
echo "lab 5 is back at its start; your work directory (~/lab05) is left as it is"
