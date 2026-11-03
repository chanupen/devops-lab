# shellcheck shell=bash
# Variables here are used by the scripts that source this file.
# shellcheck disable=SC2034
# Shared by lab 5's verify scripts. They only read: kubectl get, and
# server-side dry runs, which the API server evaluates and never stores.
set -uo pipefail

fail() {
  echo "$*"
  exit 1
}

ok() {
  echo "$*"
  exit 0
}

POLICY=require-image-digest
NS=lab05-apps
LABEL=lab.devops/lab=05-kyverno-digest
# The app image, by digest, and the same image by tag only.
DIGEST_IMAGE=ghcr.io/stefanprodan/podinfo:6.15.0@sha256:ec73780a8425f59ea49f5bc8cdff0d598805a224fbaa1f86c67a244f250fa9da
TAG_IMAGE=ghcr.io/stefanprodan/podinfo:6.15.0
KYVERNO_CLI=ghcr.io/kyverno/kyverno-cli:v1.19.2@sha256:322ec5044d600930cb3af0e56dc33154a2d1d1fba58f4cadad3f47be7e78b11c

need_kyverno() {
  kubectl version --request-timeout=5s >/dev/null 2>&1 || fail "the cluster does not answer: is the lab up? lab status"
  kubectl get crd validatingpolicies.policies.kyverno.io >/dev/null 2>&1 || fail "Kyverno is not installed: lab up --stacks policy"
}

# policy: the ValidatingPolicy as JSON; returns 1 when there is none. (Call
# it as p=$(policy) || fail ...: a fail inside $(...) ends only the subshell.)
policy() {
  kubectl get validatingpolicies.policies.kyverno.io "$POLICY" -o json 2>/dev/null
}

# dry_run_pod <name> <image>: try a Pod with a server-side dry run; prints
# the API server's answer. Nothing is stored.
dry_run_pod() {
  kubectl -n "$NS" run "$1" --image="$2" --restart=Never --dry-run=server -o name 2>&1
}
