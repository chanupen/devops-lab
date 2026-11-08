# Component licences

Inventory checked 7 Nov 2026 against `main` at `f29cb38`. This is an inventory,
not legal advice or permission to redistribute every artifact under one licence.
The repository's code and documentation are licensed under [MIT](../LICENSE),
copyright 2026 chanupen, as approved on 7 Nov 2026.
Third-party software, images and model weights retain their separate terms.

## Scope and release obligations

Pins come from both Go manifests, the Helm cache for the versions selected in
code/templates, chart dependencies, Containerfiles, lab/drill helpers and the
documented runtime/model. Charts were rendered locally with no cluster access.
Disabled bundled subcharts are listed too; they are not installed by this lab.
Future/unimplemented stacks without a pin are not represented as installed.

An image is a software bundle, not a licence grant. The tables identify its
primary software and the exact selected reference; OS packages, libraries and
plugins keep their own licences and notices. This audit did not download all
image layers or produce package-by-package SBOMs. In particular, Alpine/Debian
images include permissive and GPL/LGPL packages; NVIDIA runtime libraries have
vendor terms. Do not infer that an Apache-2.0 project makes every byte in its
image Apache-2.0.

The planned binary release must retain applicable notices and make
MPL-covered source available. GPL/AGPL obligations apply to their covered
software, not automatically to the separate `lab` executable. Review the
exact redistribution and any modifications before bundling images, models or
third-party documentation. No image or model is being republished by this PR.

## Helm charts

Version pins are in `internal/gitops`, `internal/provider/aws` and
`gitops/bootstrap/templates`; Lab 1 separately uses the podinfo Git chart.
Where an archive omits a licence file, its pinned source repository's licence
is referenced instead. Chart and application licences can differ.

| Chart | Exact version | Chart licence | Source |
| --- | --- | --- | --- |
| argo-cd | 10.10.5 | Apache-2.0 | [argo-helm](https://github.com/argoproj/argo-helm/blob/argo-cd-10.10.5/LICENSE) |
| metrics-server | 3.14.0 | Apache-2.0 | [metrics-server](https://github.com/kubernetes-sigs/metrics-server/blob/v0.9.0/LICENSE) |
| cert-manager | v1.21.2 | Apache-2.0 | [cert-manager](https://github.com/cert-manager/cert-manager/blob/v1.21.2/LICENSE) |
| gateway-helm | v1.9.2 | Apache-2.0 | [Envoy Gateway](https://github.com/envoyproxy/gateway/blob/v1.9.2/LICENSE) |
| kube-prometheus-stack | 92.10.1 | Apache-2.0 | [prometheus-community charts](https://github.com/prometheus-community/helm-charts/blob/kube-prometheus-stack-92.10.1/LICENSE) |
| loki | 7.3.0 | AGPL-3.0 | [Loki source root](https://github.com/grafana/loki/blob/helm-loki-7.3.0/LICENSE); no separate chart grant in the archive |
| opentelemetry-collector | 0.176.0 | Apache-2.0 | [OpenTelemetry charts](https://github.com/open-telemetry/opentelemetry-helm-charts/blob/opentelemetry-collector-0.176.0/LICENSE) |
| podinfo | 6.15.0; Git chart 6.17.0 in Lab 1 | Apache-2.0 | Archive `podinfo/LICENSE`; [6.17.0](https://github.com/stefanprodan/podinfo/blob/6.17.0/LICENSE) |
| kyverno; kyverno-policies | 3.9.2 each | Apache-2.0 | [Kyverno](https://github.com/kyverno/kyverno/blob/v1.19.2/LICENSE) |
| external-secrets | 2.13.0 | Apache-2.0 | [ESO](https://github.com/external-secrets/external-secrets/blob/v2.13.0/LICENSE) |
| langfuse | 2.2.3 | MIT | [Langfuse charts](https://github.com/langfuse/langfuse-k8s/blob/langfuse-2.2.3/LICENSE) |
| clickhouse-operator-helm | 0.0.8 | Apache-2.0 | [ClickHouse operator](https://github.com/ClickHouse/clickhouse-operator/blob/v0.0.8/LICENSE) |
| karpenter | 1.15.1 | Apache-2.0 | [Karpenter AWS provider](https://github.com/aws/karpenter-provider-aws/blob/v1.15.1/LICENSE) |
| aws-load-balancer-controller | 3.6.2 | Apache-2.0 | [controller](https://github.com/kubernetes-sigs/aws-load-balancer-controller/blob/v3.6.2/LICENSE) |
| aws-ebs-csi-driver | 2.67.1 | Apache-2.0 | [driver](https://github.com/kubernetes-sigs/aws-ebs-csi-driver/blob/v1.67.1/LICENSE) |

### Bundled subcharts

These exact versions were read from the cached chart archives, not inferred
from version ranges. A disabled dependency still needs review before enabling.

| Parent | Bundled chart/version | Chart licence and source |
| --- | --- | --- |
| Argo CD | redis-ha 4.38.0 (disabled) | [Apache-2.0](https://github.com/DandyDeveloper/charts/blob/redis-ha-4.38.0/LICENSE) |
| Envoy Gateway | crds 0.0.0 | Apache-2.0, parent source |
| kube-prometheus-stack | crds 0.0.0; kube-state-metrics 8.6.0; prometheus-node-exporter 4.60.0; prometheus-windows-exporter 0.12.9 (disabled) | Apache-2.0, prometheus-community charts source |
| kube-prometheus-stack | grafana 14.0.1 | [Apache-2.0 chart](https://github.com/grafana-community/helm-charts/blob/grafana-14.0.1/LICENSE), not Grafana's AGPL application licence |
| Kyverno | crds 3.9.2; grafana 3.9.2 (dashboard templates, disabled) | Apache-2.0, parent source |
| Kyverno | kyverno-api 0.0.1-alpha.4 | [Apache-2.0](https://github.com/kyverno/api/blob/v0.0.1-alpha.4/LICENSE) |
| Kyverno | openreports 0.1.0 (app 1.0.0); reports-server 0.1.6 (disabled) | [OpenReports Apache-2.0](https://github.com/openreports/reports-api/blob/main/LICENSE), chart-to-source revision correlation pending; reports-server [Apache-2.0](https://github.com/kyverno/reports-server/blob/v0.1.6/LICENSE) |
| Kyverno reports-server | postgresql 13.4.1; common 2.14.1 (disabled) | Apache-2.0, [Bitnami charts](https://github.com/bitnami/charts/blob/main/LICENSE.md); these archived versions are not deployed |
| ESO | bitwarden-sdk-server v0.6.0 (disabled) | [Apache-2.0 wrapper/chart](https://github.com/external-secrets/bitwarden-sdk-server/blob/v0.6.0/LICENSE); not a blanket licence for Bitwarden SDK features |
| Langfuse | postgres 1.6.3 | [MIT chart](https://github.com/groundhog2k/helm-charts/blob/postgres-1.6.3/LICENSE), PostgreSQL application licence separately |
| Langfuse | valkey 0.9.4 | [BSD-3-Clause chart](https://github.com/valkey-io/valkey-helm/blob/valkey-0.9.4/LICENSE) |
| Langfuse | seaweedfs 4.23.0 (disabled) | [Apache-2.0](https://github.com/seaweedfs/seaweedfs/blob/4.23/LICENSE) |
| Loki | minio 5.4.0 (disabled) | AGPL-3.0 source root; separate chart grant needs confirmation before redistribution |
| Loki | grafana-agent-operator 0.5.2 (disabled); rollout-operator 0.50.0 (disabled); rollout crds 0.1.0 | [Apache-2.0 chart repository](https://github.com/grafana/helm-charts/blob/grafana-agent-operator-0.5.2/LICENSE) |

The repository's own charts (`gitops/bootstrap`, `sample-db`, `ai/ollama`,
`ai/assist`, all version 0.1.0) are covered by [MIT](../LICENSE).

## Software behind image pins

The generated image table below contains chart-rendered and explicit
repository references. The primary-software licences are:

| Software | Licence and authoritative reference |
| --- | --- |
| Argo CD, Dex | Apache-2.0; [Argo CD](https://github.com/argoproj/argo-cd/blob/v3.5.5/LICENSE), [Dex](https://github.com/dexidp/dex/blob/v2.46.0/LICENSE) |
| cert-manager and cmctl | Apache-2.0; [cert-manager](https://github.com/cert-manager/cert-manager/blob/v1.21.2/LICENSE), [cmctl](https://github.com/cert-manager/cmctl/blob/v2.6.1/LICENSE) |
| Envoy Gateway and Envoy | Apache-2.0; [Gateway](https://github.com/envoyproxy/gateway/blob/v1.9.2/LICENSE), [Envoy](https://github.com/envoyproxy/envoy/blob/main/LICENSE) |
| Prometheus, Alertmanager, node-exporter, kube-state-metrics, operator and config-reloader | Apache-2.0; respective [Prometheus](https://github.com/prometheus), [operator](https://github.com/prometheus-operator/prometheus-operator/blob/v0.94.1/LICENSE) and [kube-state-metrics](https://github.com/kubernetes/kube-state-metrics/blob/v2.20.0/LICENSE) sources |
| Grafana 13.2.4 | [AGPL-3.0](https://github.com/grafana/grafana/blob/v13.2.4/LICENSE) |
| Loki 3.6.11 | [AGPL-3.0](https://github.com/grafana/loki/blob/v3.6.11/LICENSE); chart appVersion is 3.6.12 but the selected image is 3.6.11 |
| k8s-sidecar 2.14.1 / 2.5.0 | [MIT](https://github.com/kiwigrid/k8s-sidecar/blob/2.14.1/LICENSE) |
| kube-webhook-certgen 1.11.0 | [Apache-2.0](https://github.com/jkroepke/kube-webhook-certgen/blob/v1.11.0/LICENSE); image tag is 1.11.0, source uses v-prefixed tags |
| Metrics Server, Kyverno, ESO, OpenTelemetry Collector, AWS controllers and CSI sidecars | Apache-2.0, corresponding pinned upstream sources; AWS `eksbuild.2` builds also retain their package notices |
| Redis 8.6.4 | [AGPLv3 OR RSALv2 OR SSPLv1](https://github.com/redis/redis/blob/8.6.4/LICENSE.txt), not BSD-only |
| Langfuse web and worker 4.75.0 | [MIT core plus separate commercial EE terms](https://github.com/langfuse/langfuse/blob/v4.75.0/LICENSE); [EE restrictions](https://github.com/langfuse/langfuse/blob/v4.75.0/ee/LICENSE) |
| ClickHouse server/Keeper 26.8.36.3 and operator v0.0.8 | Apache-2.0; [server source](https://github.com/ClickHouse/ClickHouse/blob/master/LICENSE), [operator](https://github.com/ClickHouse/clickhouse-operator/blob/v0.0.8/LICENSE); server tag/source correlation needs confirmation |
| PostgreSQL 18 / 18.6 | [PostgreSQL](https://github.com/postgres/postgres/blob/REL_18_6/COPYRIGHT), plus image OS packages |
| Valkey 8.0 | [BSD-3-Clause and bundled notices](https://github.com/valkey-io/valkey/blob/8.0.0/COPYING) |
| Moto 5.2.5 | [Apache-2.0 pinned source](https://github.com/getmoto/moto/blob/5.2.5/LICENSE), also verified in [publisher metadata](https://pypi.org/pypi/moto/5.2.5/json); source moved from the old `motodotorg/moto` location |
| Zot 2.1.22 | [Apache-2.0](https://github.com/project-zot/zot/blob/v2.1.22/LICENSE) |
| podinfo 6.15.0 / 6.17.0 and the deliberately broken derivative | [Apache-2.0](https://github.com/stefanprodan/podinfo/blob/6.17.0/LICENSE); derivative keeps the original licence |
| AWS CLI 2.38.3 | [Apache-2.0](https://github.com/aws/aws-cli/blob/2.38.3/LICENSE.txt) |
| Trivy 0.76.1, Skopeo 1.22.3, Sloth 0.16.0, netshoot 0.15 | [Apache-2.0](https://github.com/aquasecurity/trivy/blob/v0.76.1/LICENSE), [Skopeo](https://github.com/containers/skopeo/blob/v1.22.3/LICENSE), [Sloth](https://github.com/slok/sloth/blob/v0.16.0/LICENSE), [netshoot](https://github.com/nicolaka/netshoot/blob/v0.15/LICENSE); netshoot bundles tools with other licences |
| curl 8.17.0 | [curl](https://github.com/curl/curl/blob/curl-8_17_0/COPYING) |
| Go 1.27.3 builder | BSD-3-Clause for Go; Alpine packages separately licensed |
| Python 3.13.18 helper image | [PSF-2.0 and bundled Python notices](https://github.com/python/cpython/blob/v3.13.18/LICENSE), plus Alpine packages |
| Alpine 3.18.0 / 3.24.3, distroless static-debian12 | Package-specific licences, not one project-wide SPDX grant; [distroless Apache-2.0 build code](https://github.com/GoogleContainerTools/distroless/blob/main/LICENSE) does not replace Debian package copyrights |
| CUDA 12.6.3-base-ubi9 (documented GPU check), Ollama's bundled GPU libraries | NVIDIA CUDA terms and base-image package licences; not covered by Ollama's MIT licence |
| Locally built sample-db / assist-receiver | MIT repository code, plus their Go dependency and base-image notices |

### kind node images and built-in addons

kind and Kubernetes are Apache-2.0. The node base is Debian trixie-slim with
package-specific terms and copied notices under `/LICENSES` and Debian package
copyrights. See the [kind v0.33.0 base build](https://github.com/kubernetes-sigs/kind/blob/v0.33.0/images/base/Dockerfile)
and [release pins](https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0).

| Kubernetes | Exact image reference |
| --- | --- |
| 1.34.11 | `kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d` |
| 1.35.8 | `kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0` |
| 1.36.4 (default) | `kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed` |
| 1.37.0 (kind library default) | `kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5` |

kind's built-in kindnet uses
`docker.io/kindest/kindnetd:v20260820-69b56db7` (Apache-2.0,
[pinned source](https://github.com/kubernetes-sigs/kind/blob/v0.33.0/pkg/build/nodeimage/const_cni.go)).
The bundled storage images are
`docker.io/kindest/local-path-provisioner:v20260820-69b56db7` and
`docker.io/kindest/local-path-helper:v20260131-7181c60a`
([Apache-2.0 build code and pinned references](https://github.com/kubernetes-sigs/kind/blob/v0.33.0/pkg/build/nodeimage/const_storage.go),
plus each helper's package licences). The base build reference is
`docker.io/kindest/base:v20260820-69b56db7`
([source](https://github.com/kubernetes-sigs/kind/blob/v0.33.0/pkg/build/nodeimage/defaults.go)).
The node bundle also includes containerd, runc, Kubernetes binaries and their
notices; the image as a whole is not Apache-2.0-only.

## Model and infrastructure terms

| Component | Pin/reference | Licence |
| --- | --- | --- |
| Ollama host runtime | `docker.io/ollama/ollama:0.40.13@sha256:b2f0122c476bc9fe7ce679d8bb4bd555351e25e2554eefb156f61cb0a89cf429` | [MIT](https://github.com/ollama/ollama/blob/v0.40.13/LICENSE); model and GPU libraries separately licensed |
| Llama 3.2 3B model | `llama3.2:3b` (mutable model tag, not a weight digest) | [Llama 3.2 Community License](https://github.com/meta-llama/llama-models/blob/main/models/llama3_2/LICENSE) and acceptable-use terms, not MIT/Apache-2.0 |
| Qwen2.5 3B comparison in #40 | `qwen2.5:3b`, eval comparison only; never a default or bundled | [Qwen Research License](https://huggingface.co/Qwen/Qwen2.5-3B-Instruct/blob/main/LICENSE), non-commercial research/evaluation only; not Apache-2.0 |
| AWS Terraform provider | 6.71.0 in all three `.terraform.lock.hcl` files | [MPL-2.0](https://github.com/hashicorp/terraform-provider-aws/blob/v6.71.0/LICENSE) |
| OpenTofu / Terraform executable | User-installed, not pinned or shipped by this repository | OpenTofu MPL-2.0; Terraform modern releases have BUSL-1.1 terms; terraform-exec's MPL-2.0 does not licence the executable |
| gitleaks used for this audit | 8.30.1, session-local tool only | [MIT](https://github.com/gitleaks/gitleaks/blob/v8.30.1/LICENSE) |
| actionlint for release validation | 1.7.12, session-local tool only | [MIT](https://github.com/rhysd/actionlint/blob/v1.7.12/LICENSE.txt) |

Qwen2.5 3B is used only as a non-commercial research eval comparison.
It must never become a default assistant model.

### Pinned CI tooling

These tools are not linked into `lab` or bundled with release binaries.

| Component | Exact workflow pin | Licence |
| --- | --- | --- |
| actions/checkout v5.1.0 | `fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09` | MIT |
| actions/setup-go v6.5.0 | `924ae3a1cded613372ab5595356fb5720e22ba16` | MIT |
| actions/cache restore/save v6.1.0 | `55cc8345863c7cc4c66a329aec7e433d2d1c52a9` | MIT |
| golangci-lint-action v9.3.0 | `ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a` | [MIT action](https://github.com/golangci/golangci-lint-action/blob/ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a/LICENSE) |
| golangci-lint | v2.15.0 | [GPL-3.0](https://github.com/golangci/golangci-lint/blob/v2.15.0/LICENSE) |
| actions/upload-artifact v7.0.2 (release) | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` | [MIT](https://github.com/actions/upload-artifact/blob/v7.0.2/LICENSE) |
| actions/download-artifact v8.0.2 (release) | `9000827ccba6bdab643e8b6fd33ac0654aef8333` | [MIT](https://github.com/actions/download-artifact/blob/v8.0.2/LICENSE) |

## Verification caveats

Do not publish a blanket "all dependencies are MIT/Apache-2.0" statement.
Confirm the specifically marked source gaps, image SBOMs and model terms before
redistributing them. No dependency, model or image was replaced during this
audit; licence-driven substitutions change behavior and require a separate
decision. The release workflow packages the repository licence, toolchain notices and
licences/notices for the module versions actually linked into both binary
targets. It includes source archive references. This does not waive the
README-only declaration caveat or authorize redistribution of images/models.

## Generated image reference inventory

There are 67 distinct chart-rendered/explicit references below. A tag-only
reference and its digest-pinned form can both occur in the repository.
Controller-created images are not Helm Deployment outputs: Envoy Gateway
v1.9.2 additionally pins
`docker.io/envoyproxy/envoy:distroless-v1.39.1@sha256:eb2c01c13125d1629637cb4e4cce7207009fb7cc2c8027f9742758549d15b6f4`
(Apache-2.0 primary code,
[exact Gateway source](https://github.com/envoyproxy/gateway/blob/v1.9.2/api/v1alpha1/shared_types.go)).
kind's built-in addon pins are listed separately above.

| Image reference | Primary software | Primary terms (not whole-image grant) | Pin source |
| --- | --- | --- | --- |
| `docker.io/amazon/aws-cli:2.38.3@sha256:efe32e825cf203652eaca3e7e94679580274e7d567c39270aaa36a5144737108` | amazon/aws-cli | Apache-2.0 | labs/02-image-promotion/steps/lib.sh; labs/04-secret-rotation/README.md; labs/04-secret-rotation/steps/lib.sh; labs/06-karpenter/README.md; labs/06-karpenter/steps/lib.sh |
| `docker.io/clickhouse/clickhouse-keeper:26.8.36.3@sha256:a847f2f30ac6950828d6f2413828adeeee4c9d261cb88f88eb6f1606a5f5a062` | clickhouse | Apache-2.0 | gitops/stacks/ai/langfuse/values.yaml |
| `docker.io/clickhouse/clickhouse-server:26.8.36.3@sha256:d426d2f612e1e189c90f9a3f8dcd85a03c6851273833817a296bb327e63aa7a7` | clickhouse | Apache-2.0 | gitops/stacks/ai/langfuse/values.yaml |
| `docker.io/curlimages/curl:8.17.0@sha256:935d9100e9ba842cdb060de42472c7ca90cfe9a7c96e4dacb55e79e560b3ff40` | curl | curl | gitops/stacks/sample-db/values.yaml; labs/03-slo-burn-rate/README.md; labs/03-slo-burn-rate/solution/01-load.yaml; labs/03-slo-burn-rate/solution/05-errors.yaml |
| `docker.io/envoyproxy/gateway:v1.9.2` | envoyproxy | Apache-2.0 | chart gateway-helm v1.9.2 |
| `docker.io/grafana/grafana:13.2.4-distroless` | Grafana | AGPL-3.0 | chart kube-prometheus-stack 92.10.1 |
| `docker.io/grafana/loki:3.6.11` | Loki | AGPL-3.0 | chart loki 7.3.0 |
| `docker.io/kiwigrid/k8s-sidecar:2.5.0` | k8s-sidecar | MIT | chart loki 7.3.0 |
| `docker.io/langfuse/langfuse-worker:4.75.0@sha256:5fd09700a8af5328f3ef004ca8ce5602f6e799aba13da5054d9b18ba1724533e` | Langfuse | MIT core; commercial EE portions | chart langfuse 2.2.3; gitops/stacks/ai/langfuse/values.yaml |
| `docker.io/langfuse/langfuse:4.75.0@sha256:23378ae8cdf1d92b647d68b81c0d0cea3632b43e9fbcbe9b765420af9c9c8662` | Langfuse | MIT core; commercial EE portions | chart langfuse 2.2.3; gitops/stacks/ai/langfuse/values.yaml |
| `docker.io/library/alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a` | Alpine | package-specific OS terms | labs/02-image-promotion/README.md; labs/02-image-promotion/steps/lib.sh; labs/04-secret-rotation/app/traffic.yaml |
| `docker.io/library/golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb` | Go builder / Alpine | BSD-3-Clause Go; package-specific OS terms | apps/sample-db/Containerfile; cmd/assist-receiver/Containerfile |
| `docker.io/library/postgres:18.6-alpine3.24@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5` | PostgreSQL | PostgreSQL | gitops/stacks/sample-db/values.yaml; labs/04-secret-rotation/app/postgres.yaml |
| `docker.io/library/postgres:18@sha256:bd5dfa494a5a259704365813dbfa5fa6b3e69be7348d43b547f540c730478583` | PostgreSQL | PostgreSQL | chart langfuse 2.2.3 |
| `docker.io/library/python:3.13.18-alpine3.24@sha256:1e1f6334529d1fb26a2ef619d7cffb30a7c7d7cc1cc7338ebee283a58d1aeaca` | Python / Alpine | PSF-2.0; bundled and OS package licences | internal/ai/smoke.go |
| `docker.io/motoserver/moto:5.2.5@sha256:59a1965d845249b04def37a0e2565f9cc6c44543cb89b68e90f28f344b3b0d1a` | motoserver/moto | Apache-2.0 | internal/provider/local/moto.go |
| `docker.io/nicolaka/netshoot:v0.15@sha256:47b907d662d139d1e2f22bfe14f4efca1e3f1feed283572f47c970c780c03b61` | nicolaka/netshoot | Apache-2.0 | drills/slow-db/README.md; drills/slow-db/drill.yaml; drills/slow-db/solution.sh |
| `docker.io/nvidia/cuda:12.6.3-base-ubi9` | NVIDIA CUDA | NVIDIA CUDA terms; UBI package terms | docs/design.md |
| `docker.io/ollama/ollama:0.40.13` | Ollama | MIT runtime; GPU/model terms separately | docs/design.md; internal/compat/compat.go |
| `docker.io/valkey/valkey:8.0@sha256:04b9002ae24bbe66687fb025052ed9bcebb54f8da3937338eb718d98d362887f` | Valkey | BSD-3-Clause; bundled notices | chart langfuse 2.2.3 |
| `ecr-public.aws.com/docker/library/redis:8.6.4-alpine` | Redis | AGPLv3 OR RSALv2 OR SSPLv1 | chart argo-cd 10.10.5 |
| `gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab` | distroless / Debian | package-specific; Apache-2.0 build code | apps/sample-db/Containerfile; cmd/assist-receiver/Containerfile |
| `ghcr.io/aquasecurity/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c` | aquasecurity/trivy | Apache-2.0 | labs/02-image-promotion/README.md; labs/02-image-promotion/steps/lib.sh |
| `ghcr.io/clickhouse/clickhouse-operator@sha256:80ff894a42fbc34fc40016776a5ba340b126b2ff408011a0af375d18c0dc8e44` | clickhouse | Apache-2.0 | chart clickhouse-operator-helm 0.0.8 |
| `ghcr.io/external-secrets/external-secrets:v2.13.0` | external-secrets | Apache-2.0 | chart external-secrets 2.13.0 |
| `ghcr.io/jkroepke/kube-webhook-certgen:1.11.0` | kube-webhook-certgen | Apache-2.0 | chart kube-prometheus-stack 92.10.1 |
| `ghcr.io/kyverno/kyverno-cli:v1.19.2@sha256:322ec5044d600930cb3af0e56dc33154a2d1d1fba58f4cadad3f47be7e78b11c` | kyverno | Apache-2.0 | labs/05-kyverno-digest/README.md; labs/05-kyverno-digest/steps/lib.sh |
| `ghcr.io/kyverno/readiness-checker:v1.19.2` | kyverno | Apache-2.0 | chart kyverno 3.9.2 |
| `ghcr.io/project-zot/zot-linux-amd64:v2.1.22@sha256:46f688dc26315a35a247e1784368829e66a67bf91de94c7d8d1645044fac1d5b` | project-zot | Apache-2.0 | internal/provider/local/zot.go |
| `ghcr.io/slok/sloth:v0.16.0@sha256:f0f0075b0d45c1cf684e92947508cc1d5bf573925f785f803a439b2306c8b9a5` | slok/sloth | Apache-2.0 | labs/03-slo-burn-rate/README.md; labs/03-slo-burn-rate/solution/07-solve.sh |
| `ghcr.io/stefanprodan/podinfo:6.15.0` | stefanprodan/podinfo | Apache-2.0 | chart podinfo 6.15.0; drills/bad-deploy/inject.sh; labs/05-kyverno-digest/README.md; labs/05-kyverno-digest/solution/02-solve.sh; labs/05-kyverno-digest/solution/04-legacy.yaml; labs/05-kyverno-digest/solution/06-resources.yaml; labs/05-kyverno-digest/steps/lib.sh |
| `ghcr.io/stefanprodan/podinfo:6.15.0@sha256:ec73780a8425f59ea49f5bc8cdff0d598805a224fbaa1f86c67a244f250fa9da` | stefanprodan/podinfo | Apache-2.0 | labs/05-kyverno-digest/solution/03-solve.sh; labs/05-kyverno-digest/solution/05-debug.yaml; labs/05-kyverno-digest/solution/06-resources.yaml; labs/05-kyverno-digest/steps/lib.sh |
| `kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d` | kind / Kubernetes / node bundles | Apache-2.0 build/code; package-specific terms | internal/provider/local/kind.go |
| `kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0` | kind / Kubernetes / node bundles | Apache-2.0 build/code; package-specific terms | internal/provider/local/kind.go |
| `kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed` | kind / Kubernetes / node bundles | Apache-2.0 build/code; package-specific terms | internal/provider/local/kind.go |
| `kindest/node:v1.37.0` | kind / Kubernetes / node bundles | Apache-2.0 build/code; package-specific terms | internal/provider/local/kind.go |
| `library/postgres:18@sha256:bd5dfa494a5a259704365813dbfa5fa6b3e69be7348d43b547f540c730478583` | PostgreSQL | PostgreSQL | gitops/stacks/ai/langfuse/values.yaml |
| `otel/opentelemetry-collector-k8s:0.162.0` | opentelemetry | Apache-2.0 | chart opentelemetry-collector 0.176.0 |
| `public.ecr.aws/csi-components/csi-attacher:v4.13.0-eksbuild.2` | csi-components | Apache-2.0 | chart aws-ebs-csi-driver 2.67.1 |
| `public.ecr.aws/csi-components/csi-node-driver-registrar:v2.18.0-eksbuild.2` | csi-components | Apache-2.0 | chart aws-ebs-csi-driver 2.67.1 |
| `public.ecr.aws/csi-components/csi-provisioner:v6.4.0-eksbuild.2` | csi-components | Apache-2.0 | chart aws-ebs-csi-driver 2.67.1 |
| `public.ecr.aws/csi-components/csi-resizer:v2.3.0-eksbuild.2` | csi-components | Apache-2.0 | chart aws-ebs-csi-driver 2.67.1 |
| `public.ecr.aws/csi-components/livenessprobe:v2.20.0-eksbuild.2` | csi-components | Apache-2.0 | chart aws-ebs-csi-driver 2.67.1 |
| `public.ecr.aws/ebs-csi-driver/aws-ebs-csi-driver:v1.67.1` | ebs-csi-driver | Apache-2.0 | chart aws-ebs-csi-driver 2.67.1 |
| `public.ecr.aws/eks/aws-load-balancer-controller:v3.6.2` | aws-load-balancer-controller | Apache-2.0 | chart aws-load-balancer-controller 3.6.2 |
| `public.ecr.aws/karpenter/controller:1.15.1@sha256:8314ff9c6bca66372031e80532c18025bbe6d34eae2443849d9ff02796fb09c8` | karpenter | Apache-2.0 | chart karpenter 1.15.1 |
| `quay.io/argoproj/argocd:v3.5.5` | argoproj/argocd | Apache-2.0 | chart argo-cd 10.10.5 |
| `quay.io/jetstack/cert-manager-cainjector:v1.21.2` | jetstack | Apache-2.0 | chart cert-manager v1.21.2 |
| `quay.io/jetstack/cert-manager-controller:v1.21.2` | jetstack | Apache-2.0 | chart cert-manager v1.21.2 |
| `quay.io/jetstack/cert-manager-startupapicheck:v1.21.2` | jetstack | Apache-2.0 | chart cert-manager v1.21.2 |
| `quay.io/jetstack/cert-manager-webhook:v1.21.2` | jetstack | Apache-2.0 | chart cert-manager v1.21.2 |
| `quay.io/jetstack/cmctl:v2.6.1@sha256:e5b5f53fede34dc1da14b9d1dac4c01f7a6030704f4fe362b7b486304976b1b6` | jetstack | Apache-2.0 | drills/cert-expiry/drill.yaml |
| `quay.io/kiwigrid/k8s-sidecar:2.14.1` | k8s-sidecar | MIT | chart kube-prometheus-stack 92.10.1 |
| `quay.io/prometheus-operator/prometheus-operator:v0.94.1` | prometheus | Apache-2.0 | chart kube-prometheus-stack 92.10.1 |
| `quay.io/prometheus/alertmanager:v0.34.1` | prometheus | Apache-2.0 | chart kube-prometheus-stack 92.10.1 |
| `quay.io/prometheus/node-exporter:v1.12.1-distroless` | Prometheus node-exporter | Apache-2.0 | chart kube-prometheus-stack 92.10.1 |
| `quay.io/prometheus/prometheus:v3.15.0-distroless` | Prometheus | Apache-2.0 | chart kube-prometheus-stack 92.10.1 |
| `quay.io/skopeo/stable:v1.22.3@sha256:62da3f53e4b72219741bfa6681e092d30391a880b6346d20bf064778f7caa3c5` | skopeo | Apache-2.0 | labs/02-image-promotion/README.md; labs/02-image-promotion/steps/lib.sh |
| `reg.kyverno.io/kyverno/background-controller:v1.19.2` | kyverno | Apache-2.0 | chart kyverno 3.9.2 |
| `reg.kyverno.io/kyverno/cleanup-controller:v1.19.2` | kyverno | Apache-2.0 | chart kyverno 3.9.2 |
| `reg.kyverno.io/kyverno/kyverno-cli:v1.19.2` | kyverno | Apache-2.0 | chart kyverno 3.9.2 |
| `reg.kyverno.io/kyverno/kyverno:v1.19.2` | kyverno | Apache-2.0 | chart kyverno 3.9.2 |
| `reg.kyverno.io/kyverno/kyvernopre:v1.19.2` | kyverno | Apache-2.0 | chart kyverno 3.9.2 |
| `reg.kyverno.io/kyverno/reports-controller:v1.19.2` | kyverno | Apache-2.0 | chart kyverno 3.9.2 |
| `registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.20.0` | kube-state-metrics | Apache-2.0 | chart kube-prometheus-stack 92.10.1 |
| `registry.k8s.io/metrics-server/metrics-server:v0.9.0` | metrics-server | Apache-2.0 | chart metrics-server 3.14.0 |
| `registry.k8s.io/pause:3.10.1@sha256:278fb9dbcca9518083ad1e11276933a2e96f23de604a3a08cc3c80002767d24c` | registry.k8s.io/pause | Apache-2.0 | labs/06-karpenter/app/inflate.yaml |

## Go modules

Both selected module graphs are included: 367 root dependencies and 44
sample-db dependencies, totaling **388 distinct module/version rows across
371 paths**. Version differences between graphs have separate rows.
Graph-only dependencies may be tests/tools and are not necessarily linked
into a release binary. Existing cached licence/notice files verified 301 rows;
87 used exact pinned upstream commits/tags. No default-branch substitute was
used for the Go licence audit.

Fourteen rows contain MPL-2.0; covered source/notices must accompany the
appropriate executable-form distribution. Two modules have separate
CC-BY-SA-4.0 documentation, and kind's logo has separate asset terms.
`go-localereader` v0.0.1 declares MIT only in its pinned README; standalone
licence text was not available. That declaration is not a substitute for
resolving notice obligations.

The licence column is a set of applicable licences, **not** an assertion that
they are alternatives. `cache:` paths are relative to `$GOMODCACHE`; use
`go mod download -json <module>@<version>` to retrieve the pinned source.
Supplemental notices and their exact references follow the table.

| module path | exact version | verified SPDX licence set | manifest/graph membership | licence source reference(s) | qualifications |
| --- | --- | --- | --- | --- | --- |
| al.essio.dev/pkg/shellescape | v1.5.1 | MIT | go.mod:indirect | cache:al.essio.dev/pkg/shellescape@v1.5.1/LICENSE | - |
| cel.dev/expr | v0.25.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/google/cel-spec/7f3c4c513b42d471d0be9439bf5bde38f45f8404/LICENSE | - |
| cloud.google.com/go/compute/metadata | v0.3.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/googleapis/google-cloud-go/b952f41fc832fbe5ed74f47ae0e6d6f2e93448e0/LICENSE | - |
| cyphar.com/go-pathrs | v0.2.5 | MPL-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/cyphar/libpathrs/080908395e75a32a5c4dbe9b318bf93643429f9f/go-pathrs/COPYING | - |
| dario.cat/mergo | v1.0.1 | BSD-3-Clause | go.mod:indirect | cache:dario.cat/mergo@v1.0.1/LICENSE | - |
| filippo.io/edwards25519 | v1.1.0 | BSD-3-Clause | go.mod:graph-only | cache:filippo.io/edwards25519@v1.1.0/LICENSE | - |
| github.com/AdaLogics/go-fuzz-headers | v0.0.0-20230811130428-ced1acdcaa24 | Apache-2.0 | go.mod:graph-only | cache:github.com/!ada!logics/go-fuzz-headers@v0.0.0-20230811130428-ced1acdcaa24/LICENSE | - |
| github.com/Azure/go-ansiterm | v0.0.0-20250102033503-faa5f7b0171c | MIT | go.mod:indirect | cache:github.com/!azure/go-ansiterm@v0.0.0-20250102033503-faa5f7b0171c/LICENSE | - |
| github.com/BurntSushi/toml | v1.6.0 | MIT | go.mod:indirect | cache:github.com/!burnt!sushi/toml@v1.6.0/COPYING | supplemental/bundled notices listed below |
| github.com/DATA-DOG/go-sqlmock | v1.5.2 | BSD-2-Clause | go.mod:graph-only | cache:github.com/!d!a!t!a-!d!o!g/go-sqlmock@v1.5.2/LICENSE | - |
| github.com/MakeNowJust/heredoc | v1.0.0 | MIT | go.mod:indirect | cache:github.com/!make!now!just/heredoc@v1.0.0/LICENSE | - |
| github.com/Masterminds/goutils | v1.1.1 | Apache-2.0 | go.mod:indirect | cache:github.com/!masterminds/goutils@v1.1.1/LICENSE.txt | - |
| github.com/Masterminds/semver/v3 | v3.5.0 | MIT | go.mod:indirect | cache:github.com/!masterminds/semver/v3@v3.5.0/LICENSE.txt | - |
| github.com/Masterminds/sprig/v3 | v3.3.0 | MIT | go.mod:indirect | cache:github.com/!masterminds/sprig/v3@v3.3.0/LICENSE.txt | - |
| github.com/Masterminds/squirrel | v1.5.4 | MIT | go.mod:indirect | cache:github.com/!masterminds/squirrel@v1.5.4/LICENSE | - |
| github.com/Masterminds/vcs | v1.13.3 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/Masterminds/vcs/v1.13.3/LICENSE.txt | - |
| github.com/Microsoft/go-winio | v0.6.2 | MIT | go.mod:graph-only | cache:github.com/!microsoft/go-winio@v0.6.2/LICENSE | - |
| github.com/NYTimes/gziphandler | v1.1.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/NYTimes/gziphandler/v1.1.1/LICENSE | - |
| github.com/ProtonMail/go-crypto | v1.5.2 | BSD-3-Clause | go.mod:indirect | cache:github.com/!proton!mail/go-crypto@v1.5.2/LICENSE | - |
| github.com/alecthomas/kingpin/v2 | v2.4.0 | MIT | go.mod:graph-only, apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/alecthomas/kingpin/64d23c54e3e2a385ce12757939bed9c632d1693c/COPYING | - |
| github.com/alecthomas/units | v0.0.0-20240927000941-0f3dac36c52b | MIT | go.mod:graph-only, apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/alecthomas/units/0f3dac36c52b29c22285af9a6e6593035dadd74c/COPYING | - |
| github.com/antlr4-go/antlr/v4 | v4.13.1 | BSD-2-Clause | go.mod:graph-only | https://raw.githubusercontent.com/antlr4-go/antlr/4d7e18847d8881a176db3d7cca909e3f3942a05f/LICENSE | - |
| github.com/apparentlymart/go-textseg/v15 | v15.0.0 | Apache-2.0; MIT | go.mod:indirect | cache:github.com/apparentlymart/go-textseg/v15@v15.0.0/LICENSE | - |
| github.com/armon/go-radix | v1.0.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/armon/go-radix/v1.0.0/LICENSE | - |
| github.com/armon/go-socks5 | v0.0.0-20160902184237-e75332964ef5 | MIT | go.mod:graph-only | cache:github.com/armon/go-socks5@v0.0.0-20160902184237-e75332964ef5/LICENSE | - |
| github.com/asaskevich/govalidator | v0.0.0-20230301143203-a9d515a09cc2 | MIT | go.mod:indirect | cache:github.com/asaskevich/govalidator@v0.0.0-20230301143203-a9d515a09cc2/LICENSE | - |
| github.com/atotto/clipboard | v0.1.4 | BSD-3-Clause | go.mod:indirect | cache:github.com/atotto/clipboard@v0.1.4/LICENSE | - |
| github.com/aws/aws-lambda-go | v1.55.1 | Apache-2.0; MIT-0 | go.mod:direct | cache:github.com/aws/aws-lambda-go@v1.55.1/LICENSE; cache:github.com/aws/aws-lambda-go@v1.55.1/LICENSE-LAMBDACODE; cache:github.com/aws/aws-lambda-go@v1.55.1/LICENSE-SUMMARY | Apache-2.0 generally; MIT-0 for Lambda function code per LICENSE-SUMMARY |
| github.com/aws/aws-sdk-go-v2 | v1.47.6 | Apache-2.0; BSD-3-Clause | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2@v1.47.6/LICENSE.txt; cache:github.com/aws/aws-sdk-go-v2@v1.47.6/NOTICE.txt | supplemental/bundled notices listed below |
| github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream | v1.7.25 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream@v1.7.25/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/config | v1.33.13 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/config@v1.33.13/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/credentials | v1.20.13 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/credentials@v1.20.13/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/feature/ec2/imds | v1.20.5 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/feature/ec2/imds@v1.20.5/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/internal/configsources | v1.5.9 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/internal/configsources@v1.5.9/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 | v2.8.9 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/internal/endpoints/v2@v2.8.9/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/internal/v4a | v1.5.8 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/internal/v4a@v1.5.8/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/budgets | v1.53.5 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/budgets@v1.53.5/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/codebuild | v1.78.8 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/codebuild@v1.78.8/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/ec2 | v1.343.0 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/ec2@v1.343.0/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/ecr | v1.67.2 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/ecr@v1.67.2/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/eks | v1.105.2 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/eks@v1.105.2/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing | v1.41.6 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing@v1.41.6/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 | v1.63.6 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2@v1.63.6/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/iam | v1.64.7 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/iam@v1.64.7/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding | v1.13.23 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding@v1.13.23/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/internal/checksum | v1.11.9 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/service/internal/checksum@v1.11.9/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/internal/presigned-url | v1.14.8 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/service/internal/presigned-url@v1.14.8/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/internal/s3shared | v1.20.8 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/service/internal/s3shared@v1.20.8/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/lambda | v1.114.0 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/lambda@v1.114.0/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi | v1.41.8 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi@v1.41.8/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/s3 | v1.114.6 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/s3@v1.114.6/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/secretsmanager | v1.50.8 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/secretsmanager@v1.50.8/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/signin | v1.12.1 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/service/signin@v1.12.1/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/sns | v1.47.7 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/sns@v1.47.7/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/sso | v1.39.1 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/service/sso@v1.39.1/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/ssooidc | v1.44.1 | Apache-2.0 | go.mod:indirect | cache:github.com/aws/aws-sdk-go-v2/service/ssooidc@v1.44.1/LICENSE.txt | - |
| github.com/aws/aws-sdk-go-v2/service/sts | v1.51.6 | Apache-2.0 | go.mod:direct | cache:github.com/aws/aws-sdk-go-v2/service/sts@v1.51.6/LICENSE.txt | - |
| github.com/aws/smithy-go | v1.28.8 | Apache-2.0; BSD-3-Clause | go.mod:direct | cache:github.com/aws/smithy-go@v1.28.8/LICENSE; cache:github.com/aws/smithy-go@v1.28.8/NOTICE | supplemental/bundled notices listed below |
| github.com/aymanbagabas/go-osc52/v2 | v2.0.1 | MIT | go.mod:indirect | cache:github.com/aymanbagabas/go-osc52/v2@v2.0.1/LICENSE | - |
| github.com/aymanbagabas/go-udiff | v0.2.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/aymanbagabas/go-udiff/4ad777e23c62c40120812548719a9d734b9c39ac/LICENSE-MIT | - |
| github.com/beorn7/perks | v1.0.1 | MIT | go.mod:indirect, apps/sample-db/go.mod:indirect | cache:github.com/beorn7/perks@v1.0.1/LICENSE | - |
| github.com/bgentry/speakeasy | v0.1.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/bgentry/speakeasy/v0.1.0/LICENSE | - |
| github.com/bits-and-blooms/bitset | v1.22.0 | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/bits-and-blooms/bitset/d1501626adba4495fc0ce19cba33407a57d2d157/LICENSE | - |
| github.com/blang/semver/v4 | v4.0.0 | MIT | go.mod:indirect | cache:github.com/blang/semver/v4@v4.0.0/LICENSE | - |
| github.com/bshuster-repo/logrus-logstash-hook | v1.1.0 | MIT | go.mod:graph-only | cache:github.com/bshuster-repo/logrus-logstash-hook@v1.1.0/LICENSE | - |
| github.com/bwesterb/go-ristretto | v1.2.3 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/bwesterb/go-ristretto/7c80df9e61a54045aedb2ccad99d1e636a4b4c90/LICENSE | - |
| github.com/cenkalti/backoff/v4 | v4.3.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/cenkalti/backoff/720b78985a65c0452fd37bb155c7cac4157a7c45/LICENSE | - |
| github.com/cenkalti/backoff/v5 | v5.0.3 | MIT | go.mod:graph-only | cache:github.com/cenkalti/backoff/v5@v5.0.3/LICENSE | - |
| github.com/cespare/xxhash/v2 | v2.3.0 | MIT | go.mod:indirect, apps/sample-db/go.mod:indirect | cache:github.com/cespare/xxhash/v2@v2.3.0/LICENSE.txt | - |
| github.com/chai2010/gettext-go | v1.0.2 | BSD-3-Clause | go.mod:indirect | cache:github.com/chai2010/gettext-go@v1.0.2/LICENSE | - |
| github.com/charmbracelet/bubbles | v0.21.0 | MIT | go.mod:direct | cache:github.com/charmbracelet/bubbles@v0.21.0/LICENSE | - |
| github.com/charmbracelet/bubbletea | v1.3.10 | MIT | go.mod:direct | cache:github.com/charmbracelet/bubbletea@v1.3.10/LICENSE | - |
| github.com/charmbracelet/colorprofile | v0.2.3-0.20250311203215-f60798e515dc | MIT | go.mod:indirect | cache:github.com/charmbracelet/colorprofile@v0.2.3-0.20250311203215-f60798e515dc/LICENSE | - |
| github.com/charmbracelet/harmonica | v0.2.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/charmbracelet/harmonica/v0.2.0/LICENSE | - |
| github.com/charmbracelet/lipgloss | v1.1.0 | MIT | go.mod:direct | cache:github.com/charmbracelet/lipgloss@v1.1.0/LICENSE | - |
| github.com/charmbracelet/x/ansi | v0.10.1 | MIT | go.mod:direct | cache:github.com/charmbracelet/x/ansi@v0.10.1/LICENSE | - |
| github.com/charmbracelet/x/cellbuf | v0.0.13-0.20250311204145-2c3ea96c31dd | MIT | go.mod:indirect | cache:github.com/charmbracelet/x/cellbuf@v0.0.13-0.20250311204145-2c3ea96c31dd/LICENSE | - |
| github.com/charmbracelet/x/exp/golden | v0.0.0-20241011142426-46044092ad91 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/charmbracelet/x/46044092ad91b93dca957b7676c2b739e98720e9/LICENSE | - |
| github.com/charmbracelet/x/term | v0.2.1 | MIT | go.mod:indirect | cache:github.com/charmbracelet/x/term@v0.2.1/LICENSE | - |
| github.com/cloudflare/circl | v1.6.3 | BSD-3-Clause | go.mod:indirect | cache:github.com/cloudflare/circl@v1.6.3/LICENSE | supplemental/bundled notices listed below |
| github.com/coreos/go-oidc | v2.5.0+incompatible | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/coreos/go-oidc/153fc73f601ff388edee90ce864c564ed5195695/LICENSE; https://raw.githubusercontent.com/coreos/go-oidc/153fc73f601ff388edee90ce864c564ed5195695/NOTICE | - |
| github.com/coreos/go-semver | v0.3.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/coreos/go-semver/c16f28124668daf02b2a32a431dec2f183977ffc/LICENSE; https://raw.githubusercontent.com/coreos/go-semver/c16f28124668daf02b2a32a431dec2f183977ffc/NOTICE | - |
| github.com/coreos/go-systemd/v22 | v22.7.0 | Apache-2.0 | go.mod:graph-only | cache:github.com/coreos/go-systemd/v22@v22.7.0/LICENSE; cache:github.com/coreos/go-systemd/v22@v22.7.0/NOTICE | - |
| github.com/cpuguy83/go-md2man/v2 | v2.0.6 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/cpuguy83/go-md2man/441631534462b74c098953dbd8d5f6210994b0bf/LICENSE.md | - |
| github.com/creack/pty | v1.1.18 | MIT | go.mod:direct | cache:github.com/creack/pty@v1.1.18/LICENSE | - |
| github.com/cyphar/filepath-securejoin | v0.7.0 | BSD-3-Clause; MPL-2.0 | go.mod:indirect | cache:github.com/cyphar/filepath-securejoin@v0.7.0/COPYING.md; cache:github.com/cyphar/filepath-securejoin@v0.7.0/LICENSE.BSD; cache:github.com/cyphar/filepath-securejoin@v0.7.0/LICENSE.MPL-2.0 | BSD-3-Clause AND MPL-2.0 per pinned README; file-scoped copyleft |
| github.com/davecgh/go-spew | v1.1.1 | ISC | apps/sample-db/go.mod:graph-only | cache:github.com/davecgh/go-spew@v1.1.1/LICENSE | - |
| github.com/davecgh/go-spew | v1.1.2-0.20180830191138-d8f796af33cc | ISC | go.mod:indirect | cache:github.com/davecgh/go-spew@v1.1.2-0.20180830191138-d8f796af33cc/LICENSE | - |
| github.com/denisenkom/go-mssqldb | v0.9.0 | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/denisenkom/go-mssqldb/v0.9.0/LICENSE.txt | - |
| github.com/dgryski/go-rendezvous | v0.0.0-20200823014737-9f7001d12a5f | MIT | go.mod:graph-only | cache:github.com/dgryski/go-rendezvous@v0.0.0-20200823014737-9f7001d12a5f/LICENSE | - |
| github.com/distribution/distribution/v3 | v3.1.1 | Apache-2.0; MIT | go.mod:graph-only | cache:github.com/distribution/distribution/v3@v3.1.1/LICENSE | supplemental/bundled notices listed below |
| github.com/distribution/reference | v0.6.0 | Apache-2.0 | go.mod:graph-only | cache:github.com/distribution/reference@v0.6.0/LICENSE | - |
| github.com/dlclark/regexp2 | v1.11.0 | MIT | go.mod:graph-only | cache:github.com/dlclark/regexp2@v1.11.0/LICENSE | - |
| github.com/docker/docker-credential-helpers | v0.9.5 | MIT | go.mod:graph-only | cache:github.com/docker/docker-credential-helpers@v0.9.5/LICENSE | - |
| github.com/docker/go-events | v0.0.0-20250808211157-605354379745 | Apache-2.0 | go.mod:graph-only | cache:github.com/docker/go-events@v0.0.0-20250808211157-605354379745/LICENSE | - |
| github.com/docker/go-metrics | v0.0.1 | Apache-2.0 | go.mod:graph-only | cache:github.com/docker/go-metrics@v0.0.1/LICENSE; cache:github.com/docker/go-metrics@v0.0.1/LICENSE.docs; cache:github.com/docker/go-metrics@v0.0.1/NOTICE | documentation: CC-BY-SA-4.0 (not Go code) |
| github.com/dustin/go-humanize | v1.0.1 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/dustin/go-humanize/9ec74ab2f7a7161664182fd4e5e292fccffbc75f/LICENSE | - |
| github.com/dylibso/observe-sdk/go | v0.0.0-20240819160327-2d926c5d788a | Apache-2.0 | go.mod:indirect | cache:github.com/dylibso/observe-sdk/go@v0.0.0-20240819160327-2d926c5d788a/LICENSE | - |
| github.com/emicklei/go-restful/v3 | v3.13.0 | MIT | go.mod:indirect | cache:github.com/emicklei/go-restful/v3@v3.13.0/LICENSE | - |
| github.com/emirpasic/gods | v1.18.1 | BSD-2-Clause; ISC | go.mod:graph-only | cache:github.com/emirpasic/gods@v1.18.1/LICENSE | - |
| github.com/erikgeiser/coninput | v0.0.0-20211004153227-1c3628e74d0f | MIT | go.mod:indirect | cache:github.com/erikgeiser/coninput@v0.0.0-20211004153227-1c3628e74d0f/LICENSE | - |
| github.com/evanphx/json-patch/v5 | v5.9.11 | BSD-3-Clause | go.mod:indirect | cache:github.com/evanphx/json-patch/v5@v5.9.11/LICENSE | - |
| github.com/exponent-io/jsonpath | v0.0.0-20210407135951-1de76d718b3f | MIT | go.mod:indirect | cache:github.com/exponent-io/jsonpath@v0.0.0-20210407135951-1de76d718b3f/LICENSE | - |
| github.com/extism/go-sdk | v1.7.1 | BSD-3-Clause | go.mod:indirect | cache:github.com/extism/go-sdk@v1.7.1/LICENSE | - |
| github.com/fatih/camelcase | v1.0.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/fatih/camelcase/v1.0.0/LICENSE.md | - |
| github.com/fatih/color | v1.19.0 | MIT | go.mod:indirect | cache:github.com/fatih/color@v1.19.0/LICENSE.md | - |
| github.com/felixge/httpsnoop | v1.0.4 | MIT | go.mod:graph-only | cache:github.com/felixge/httpsnoop@v1.0.4/LICENSE.txt | - |
| github.com/fluxcd/cli-utils | v1.2.2 | Apache-2.0 | go.mod:indirect | cache:github.com/fluxcd/cli-utils@v1.2.2/LICENSE | - |
| github.com/foxcpp/go-mockdns | v1.2.0 | MIT | go.mod:graph-only | cache:github.com/foxcpp/go-mockdns@v1.2.0/LICENSE | - |
| github.com/frankban/quicktest | v1.14.6 | MIT | go.mod:graph-only | cache:github.com/frankban/quicktest@v1.14.6/LICENSE | - |
| github.com/fsnotify/fsnotify | v1.9.0 | BSD-3-Clause | go.mod:graph-only | cache:github.com/fsnotify/fsnotify@v1.9.0/LICENSE | - |
| github.com/fxamacker/cbor/v2 | v2.9.1 | MIT | go.mod:indirect | cache:github.com/fxamacker/cbor/v2@v2.9.1/LICENSE | - |
| github.com/go-errors/errors | v1.5.1 | MIT | go.mod:indirect | cache:github.com/go-errors/errors@v1.5.1/LICENSE.MIT | - |
| github.com/go-git/gcfg | v1.5.1-0.20230307220236-3a3c6141e376 | BSD-3-Clause | go.mod:graph-only | cache:github.com/go-git/gcfg@v1.5.1-0.20230307220236-3a3c6141e376/LICENSE | - |
| github.com/go-git/go-billy/v5 | v5.9.0 | Apache-2.0 | go.mod:graph-only | cache:github.com/go-git/go-billy/v5@v5.9.0/LICENSE | - |
| github.com/go-git/go-git/v5 | v5.19.2 | Apache-2.0 | go.mod:graph-only | cache:github.com/go-git/go-git/v5@v5.19.2/LICENSE | - |
| github.com/go-gorp/gorp/v3 | v3.1.0 | MIT | go.mod:indirect | cache:github.com/go-gorp/gorp/v3@v3.1.0/LICENSE | - |
| github.com/go-jose/go-jose/v4 | v4.1.4 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:github.com/go-jose/go-jose/v4@v4.1.4/LICENSE | supplemental/bundled notices listed below |
| github.com/go-logfmt/logfmt | v0.6.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/go-logfmt/logfmt/76262ea710c6213a336b12b0356fec81341935e1/LICENSE | - |
| github.com/go-logr/logr | v1.4.4 | Apache-2.0 | go.mod:direct | cache:github.com/go-logr/logr@v1.4.4/LICENSE | - |
| github.com/go-logr/stdr | v1.2.2 | Apache-2.0 | go.mod:graph-only | cache:github.com/go-logr/stdr@v1.2.2/LICENSE | - |
| github.com/go-logr/zapr | v1.3.0 | Apache-2.0 | go.mod:graph-only | cache:github.com/go-logr/zapr@v1.3.0/LICENSE | - |
| github.com/go-openapi/jsonpointer | v1.0.0 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/jsonpointer@v1.0.0/LICENSE; cache:github.com/go-openapi/jsonpointer@v1.0.0/NOTICE | - |
| github.com/go-openapi/jsonreference | v1.0.0 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/jsonreference@v1.0.0/LICENSE; cache:github.com/go-openapi/jsonreference@v1.0.0/NOTICE | - |
| github.com/go-openapi/swag | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/cmdutils | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/cmdutils@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/conv | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/conv@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/fileutils | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/fileutils@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/jsonutils | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/jsonutils@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/jsonutils/fixtures_test | v0.27.1 | Apache-2.0 | go.mod:graph-only | cache:github.com/go-openapi/swag/jsonutils/fixtures_test@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/loading | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/loading@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/mangling | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/mangling@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/netutils | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/netutils@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/pools | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/pools@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/stringutils | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/stringutils@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/typeutils | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/typeutils@v0.27.1/LICENSE | - |
| github.com/go-openapi/swag/yamlutils | v0.27.1 | Apache-2.0 | go.mod:indirect | cache:github.com/go-openapi/swag/yamlutils@v0.27.1/LICENSE | - |
| github.com/go-openapi/testify/enable/yaml/v2 | v2.6.0 | Apache-2.0 | go.mod:graph-only | cache:github.com/go-openapi/testify/enable/yaml/v2@v2.6.0/LICENSE | - |
| github.com/go-openapi/testify/v2 | v2.6.0 | Apache-2.0; BSD-2-Clause; ISC; MIT | go.mod:graph-only | cache:github.com/go-openapi/testify/v2@v2.6.0/LICENSE; cache:github.com/go-openapi/testify/v2@v2.6.0/NOTICE | supplemental/bundled notices listed below |
| github.com/go-sql-driver/mysql | v1.8.1 | MPL-2.0 | go.mod:graph-only | cache:github.com/go-sql-driver/mysql@v1.8.1/LICENSE | - |
| github.com/go-task/slim-sprig/v3 | v3.0.0 | MIT | go.mod:graph-only | cache:github.com/go-task/slim-sprig/v3@v3.0.0/LICENSE.txt | - |
| github.com/gobwas/glob | v0.2.3 | MIT | go.mod:indirect | cache:github.com/gobwas/glob@v0.2.3/LICENSE | - |
| github.com/godror/godror | v0.40.4 | Apache-2.0; BSD-3-Clause; MIT | go.mod:graph-only | https://raw.githubusercontent.com/godror/godror/c59c697bcd726ac56bdfdfc906a1b1b2e5b97eed/LICENSE.md | - |
| github.com/godror/knownpb | v0.1.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/godror/knownpb/9746aaff4be5472bde70aa634cea260ce745a656/LICENSE | - |
| github.com/gofrs/flock | v0.13.0 | BSD-3-Clause | go.mod:indirect | cache:github.com/gofrs/flock@v0.13.0/LICENSE | - |
| github.com/golang-jwt/jwt/v5 | v5.3.1 | MIT | go.mod:graph-only, apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/golang-jwt/jwt/7ceae619e739dc8a7bf577214aa8ebf26668e9db/LICENSE | - |
| github.com/golang-sql/civil | v0.0.0-20190719163853-cb61b32ac6fe | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/golang-sql/civil/cb61b32ac6fe/LICENSE | - |
| github.com/golang/groupcache | v0.0.0-20241129210726-2c02b8208cf8 | Apache-2.0 | go.mod:graph-only | cache:github.com/golang/groupcache@v0.0.0-20241129210726-2c02b8208cf8/LICENSE | - |
| github.com/golang/protobuf | v1.5.0 | BSD-3-Clause | apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/golang/protobuf/v1.5.0/LICENSE | - |
| github.com/golang/protobuf | v1.5.4 | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/golang/protobuf/75de7c059e36b64f01d0dd234ff2fff404ec3374/LICENSE | - |
| github.com/google/btree | v1.1.3 | Apache-2.0 | go.mod:indirect | cache:github.com/google/btree@v1.1.3/LICENSE | - |
| github.com/google/cel-go | v0.29.2 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/google/cel-go/97611dc314dd41c9a4827b79f5196489f8a14201/LICENSE | - |
| github.com/google/gnostic-models | v0.7.1 | Apache-2.0 | go.mod:indirect | cache:github.com/google/gnostic-models@v0.7.1/LICENSE | - |
| github.com/google/go-cmp | v0.7.0 | BSD-3-Clause | go.mod:graph-only, apps/sample-db/go.mod:graph-only | cache:github.com/google/go-cmp@v0.7.0/LICENSE | - |
| github.com/google/gofuzz | v1.2.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/google/gofuzz/v1.2.0/LICENSE | - |
| github.com/google/pprof | v0.0.0-20260402051712-545e8a4df936 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:github.com/google/pprof@v0.0.0-20260402051712-545e8a4df936/LICENSE | supplemental/bundled notices listed below |
| github.com/google/shlex | v0.0.0-20191202100458-e7afc7fbc510 | Apache-2.0 | go.mod:graph-only | cache:github.com/google/shlex@v0.0.0-20191202100458-e7afc7fbc510/COPYING | - |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | go.mod:indirect, apps/sample-db/go.mod:graph-only | cache:github.com/google/uuid@v1.6.0/LICENSE | - |
| github.com/gorilla/handlers | v1.5.2 | BSD-3-Clause | go.mod:graph-only | cache:github.com/gorilla/handlers@v1.5.2/LICENSE | - |
| github.com/gorilla/mux | v1.8.1 | BSD-3-Clause | go.mod:graph-only | cache:github.com/gorilla/mux@v1.8.1/LICENSE | - |
| github.com/gorilla/websocket | v1.5.4-0.20250319132907-e064f32e3674 | BSD-2-Clause | go.mod:indirect | cache:github.com/gorilla/websocket@v1.5.4-0.20250319132907-e064f32e3674/LICENSE | - |
| github.com/gosuri/uitable | v0.0.4 | MIT | go.mod:indirect | cache:github.com/gosuri/uitable@v0.0.4/LICENSE | supplemental/bundled notices listed below |
| github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus | v1.1.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/grpc-ecosystem/go-grpc-middleware/53040665d19bab33e444905822557433511bb981/LICENSE | - |
| github.com/grpc-ecosystem/go-grpc-middleware/v2 | v2.3.3 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/grpc-ecosystem/go-grpc-middleware/390bcef25adebe4b0c7dbb365230c0a856737afe/LICENSE | - |
| github.com/grpc-ecosystem/grpc-gateway/v2 | v2.29.0 | BSD-3-Clause | go.mod:graph-only | cache:github.com/grpc-ecosystem/grpc-gateway/v2@v2.29.0/LICENSE | supplemental/bundled notices listed below |
| github.com/hashicorp/errwrap | v1.1.0 | MPL-2.0 | go.mod:graph-only | cache:github.com/hashicorp/errwrap@v1.1.0/LICENSE | - |
| github.com/hashicorp/go-cleanhttp | v0.5.2 | MPL-2.0 | go.mod:graph-only | cache:github.com/hashicorp/go-cleanhttp@v0.5.2/LICENSE | - |
| github.com/hashicorp/go-multierror | v1.1.1 | MPL-2.0 | go.mod:graph-only | cache:github.com/hashicorp/go-multierror@v1.1.1/LICENSE | - |
| github.com/hashicorp/go-retryablehttp | v0.7.8 | MPL-2.0 | go.mod:graph-only | cache:github.com/hashicorp/go-retryablehttp@v0.7.8/LICENSE | - |
| github.com/hashicorp/go-version | v1.9.0 | MPL-2.0 | go.mod:indirect | cache:github.com/hashicorp/go-version@v1.9.0/LICENSE | - |
| github.com/hashicorp/golang-lru/arc/v2 | v2.0.5 | MPL-2.0 | go.mod:graph-only | cache:github.com/hashicorp/golang-lru/arc/v2@v2.0.5/LICENSE | - |
| github.com/hashicorp/golang-lru/v2 | v2.0.5 | BSD-3-Clause; MPL-2.0 | go.mod:graph-only | cache:github.com/hashicorp/golang-lru/v2@v2.0.5/LICENSE | supplemental/bundled notices listed below |
| github.com/hashicorp/hc-install | v0.10.0 | MPL-2.0 | go.mod:graph-only | cache:github.com/hashicorp/hc-install@v0.10.0/LICENSE | - |
| github.com/hashicorp/terraform-exec | v0.26.0 | MPL-2.0 | go.mod:direct | cache:github.com/hashicorp/terraform-exec@v0.26.0/LICENSE | - |
| github.com/hashicorp/terraform-json | v0.29.0 | MPL-2.0 | go.mod:direct | cache:github.com/hashicorp/terraform-json@v0.29.0/LICENSE | - |
| github.com/huandu/xstrings | v1.5.0 | MIT | go.mod:indirect | cache:github.com/huandu/xstrings@v1.5.0/LICENSE | - |
| github.com/ianlancetaylor/demangle | v0.0.0-20240805132620-81f5be970eca | BSD-3-Clause | go.mod:indirect | cache:github.com/ianlancetaylor/demangle@v0.0.0-20240805132620-81f5be970eca/LICENSE | - |
| github.com/imdario/mergo | v0.3.13 | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/imdario/mergo/v0.3.13/LICENSE | - |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0 | go.mod:indirect | cache:github.com/inconshreveable/mousetrap@v1.1.0/LICENSE | - |
| github.com/jackc/pgpassfile | v1.0.0 | MIT | apps/sample-db/go.mod:indirect | cache:github.com/jackc/pgpassfile@v1.0.0/LICENSE | - |
| github.com/jackc/pgservicefile | v0.0.0-20240606120523-5a60cdf6a761 | MIT | apps/sample-db/go.mod:indirect | cache:github.com/jackc/pgservicefile@v0.0.0-20240606120523-5a60cdf6a761/LICENSE | - |
| github.com/jackc/pgx/v5 | v5.12.0 | MIT | apps/sample-db/go.mod:direct | cache:github.com/jackc/pgx/v5@v5.12.0/LICENSE | - |
| github.com/jackc/puddle/v2 | v2.2.3 | MIT | apps/sample-db/go.mod:indirect | cache:github.com/jackc/puddle/v2@v2.2.3/LICENSE | - |
| github.com/jbenet/go-context | v0.0.0-20150711004518-d14ea06fba99 | MIT | go.mod:graph-only | cache:github.com/jbenet/go-context@v0.0.0-20150711004518-d14ea06fba99/LICENSE | - |
| github.com/jessevdk/go-flags | v1.6.1 | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/jessevdk/go-flags/c02e333e441eb1187c25e6d689d769d499ec2a0b/LICENSE | - |
| github.com/jmoiron/sqlx | v1.4.0 | MIT | go.mod:indirect | cache:github.com/jmoiron/sqlx@v1.4.0/LICENSE | - |
| github.com/jonboulle/clockwork | v0.5.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/jonboulle/clockwork/6d8d032a18422c2e3ef651170a8a55012d1f704c/LICENSE | - |
| github.com/josharian/intern | v1.0.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/josharian/intern/v1.0.0/license.md | - |
| github.com/jpillora/backoff | v1.0.0 | MIT | go.mod:graph-only, apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/jpillora/backoff/v1.0.0/LICENSE | - |
| github.com/json-iterator/go | v1.1.12 | MIT | go.mod:indirect, apps/sample-db/go.mod:graph-only | cache:github.com/json-iterator/go@v1.1.12/LICENSE | - |
| github.com/julienschmidt/httprouter | v1.3.0 | BSD-3-Clause | go.mod:graph-only, apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/julienschmidt/httprouter/v1.3.0/LICENSE | - |
| github.com/kevinburke/ssh_config | v1.2.0 | MIT | go.mod:graph-only | cache:github.com/kevinburke/ssh_config@v1.2.0/LICENSE | - |
| github.com/klauspost/compress | v1.19.0 | Apache-2.0; BSD-3-Clause; MIT | go.mod:graph-only | cache:github.com/klauspost/compress@v1.19.0/LICENSE | supplemental/bundled notices listed below |
| github.com/klauspost/compress | v1.20.1 | Apache-2.0; BSD-3-Clause; MIT | apps/sample-db/go.mod:graph-only | cache:github.com/klauspost/compress@v1.20.1/LICENSE | supplemental/bundled notices listed below |
| github.com/klauspost/cpuid/v2 | v2.3.0 | MIT | go.mod:graph-only | cache:github.com/klauspost/cpuid/v2@v2.3.0/LICENSE | - |
| github.com/kr/pretty | v0.3.0 | MIT | apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/kr/pretty/v0.3.0/License | - |
| github.com/kr/pretty | v0.3.1 | MIT | go.mod:graph-only | cache:github.com/kr/pretty@v0.3.1/License | - |
| github.com/kr/text | v0.2.0 | MIT | go.mod:graph-only | cache:github.com/kr/text@v0.2.0/License | - |
| github.com/kylelemons/godebug | v1.1.0 | Apache-2.0 | go.mod:graph-only, apps/sample-db/go.mod:indirect | cache:github.com/kylelemons/godebug@v1.1.0/LICENSE | - |
| github.com/lann/builder | v0.0.0-20180802200727-47ae307949d0 | MIT | go.mod:indirect | cache:github.com/lann/builder@v0.0.0-20180802200727-47ae307949d0/LICENSE | - |
| github.com/lann/ps | v0.0.0-20150810152359-62de8c46ede0 | MIT | go.mod:indirect | cache:github.com/lann/ps@v0.0.0-20150810152359-62de8c46ede0/LICENSE | - |
| github.com/lib/pq | v1.12.3 | MIT | go.mod:indirect | cache:github.com/lib/pq@v1.12.3/LICENSE | - |
| github.com/liggitt/tabwriter | v0.0.0-20181228230101-89fcab3d43de | BSD-3-Clause | go.mod:indirect | cache:github.com/liggitt/tabwriter@v0.0.0-20181228230101-89fcab3d43de/LICENSE | - |
| github.com/lithammer/dedent | v1.1.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/lithammer/dedent/v1.1.0/LICENSE | - |
| github.com/lucasb-eyer/go-colorful | v1.2.0 | MIT | go.mod:indirect | cache:github.com/lucasb-eyer/go-colorful@v1.2.0/LICENSE | - |
| github.com/mailru/easyjson | v0.9.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/mailru/easyjson/5e854fb809ec83ff18eb74554fd5c693e28eaadb/LICENSE | - |
| github.com/mattn/go-colorable | v0.1.14 | MIT | go.mod:indirect | cache:github.com/mattn/go-colorable@v0.1.14/LICENSE | - |
| github.com/mattn/go-isatty | v0.0.20 | MIT | go.mod:direct | cache:github.com/mattn/go-isatty@v0.0.20/LICENSE | - |
| github.com/mattn/go-localereader | v0.0.1 | MIT | go.mod:indirect | https://raw.githubusercontent.com/mattn/go-localereader/v0.0.1/README.md | MIT explicitly declared in pinned README; no standalone licence text in pinned root |
| github.com/mattn/go-oci8 | v0.1.1 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/mattn/go-oci8/v0.1.1/LICENSE | - |
| github.com/mattn/go-runewidth | v0.0.16 | MIT | go.mod:indirect | cache:github.com/mattn/go-runewidth@v0.0.16/LICENSE | - |
| github.com/mattn/go-shellwords | v1.0.14 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/mattn/go-shellwords/2b31b13123b0c5253ca007689e2e11c1512e9624/LICENSE | - |
| github.com/mattn/go-sqlite3 | v1.14.22 | MIT | go.mod:graph-only | cache:github.com/mattn/go-sqlite3@v1.14.22/LICENSE | - |
| github.com/miekg/dns | v1.1.57 | BSD-3-Clause | go.mod:graph-only | cache:github.com/miekg/dns@v1.1.57/COPYRIGHT; cache:github.com/miekg/dns@v1.1.57/LICENSE | - |
| github.com/mitchellh/cli | v1.1.5 | MPL-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/mitchellh/cli/784fcd13e857c49c0e5738a19707762f372a3eb3/LICENSE | - |
| github.com/mitchellh/copystructure | v1.2.0 | MIT | go.mod:indirect | cache:github.com/mitchellh/copystructure@v1.2.0/LICENSE | - |
| github.com/mitchellh/go-wordwrap | v1.0.1 | MIT | go.mod:indirect | cache:github.com/mitchellh/go-wordwrap@v1.0.1/LICENSE.md | - |
| github.com/mitchellh/reflectwalk | v1.0.2 | MIT | go.mod:indirect | cache:github.com/mitchellh/reflectwalk@v1.0.2/LICENSE | - |
| github.com/moby/spdystream | v0.5.1 | Apache-2.0; BSD-3-Clause | go.mod:indirect | cache:github.com/moby/spdystream@v0.5.1/LICENSE; cache:github.com/moby/spdystream@v0.5.1/NOTICE | supplemental/bundled notices listed below |
| github.com/moby/term | v0.5.2 | Apache-2.0 | go.mod:indirect | cache:github.com/moby/term@v0.5.2/LICENSE | - |
| github.com/modern-go/concurrent | v0.0.0-20180306012644-bacd9c7ef1dd | Apache-2.0 | go.mod:indirect, apps/sample-db/go.mod:graph-only | cache:github.com/modern-go/concurrent@v0.0.0-20180306012644-bacd9c7ef1dd/LICENSE | - |
| github.com/modern-go/reflect2 | v1.0.2 | Apache-2.0 | apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/modern-go/reflect2/v1.0.2/LICENSE | - |
| github.com/modern-go/reflect2 | v1.0.3-0.20250322232337-35a7c28c31ee | Apache-2.0 | go.mod:indirect | cache:github.com/modern-go/reflect2@v1.0.3-0.20250322232337-35a7c28c31ee/LICENSE | - |
| github.com/monochromegane/go-gitignore | v0.0.0-20200626010858-205db1a8cc00 | MIT | go.mod:indirect | cache:github.com/monochromegane/go-gitignore@v0.0.0-20200626010858-205db1a8cc00/LICENSE | - |
| github.com/muesli/ansi | v0.0.0-20230316100256-276c6243b2f6 | MIT | go.mod:indirect | cache:github.com/muesli/ansi@v0.0.0-20230316100256-276c6243b2f6/LICENSE | - |
| github.com/muesli/cancelreader | v0.2.2 | MIT | go.mod:indirect | cache:github.com/muesli/cancelreader@v0.2.2/LICENSE | - |
| github.com/muesli/termenv | v0.16.0 | MIT | go.mod:indirect | cache:github.com/muesli/termenv@v0.16.0/LICENSE | - |
| github.com/munnerz/goautoneg | v0.0.0-20191010083416-a7dc8b61c822 | BSD-3-Clause | go.mod:indirect, apps/sample-db/go.mod:indirect | cache:github.com/munnerz/goautoneg@v0.0.0-20191010083416-a7dc8b61c822/LICENSE | - |
| github.com/mwitkow/go-conntrack | v0.0.0-20190716064945-2f068394615f | Apache-2.0 | go.mod:graph-only, apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/mwitkow/go-conntrack/2f068394615f/LICENSE | - |
| github.com/mxk/go-flowrate | v0.0.0-20140419014527-cca7078d478f | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/mxk/go-flowrate/cca7078d478f/LICENSE | - |
| github.com/nelsam/hel/v2 | v2.3.3 | Unlicense | go.mod:graph-only | https://raw.githubusercontent.com/nelsam/hel/v2.3.3/LICENSE | - |
| github.com/niemeyer/pretty | v0.0.0-20200227124842-a10e7caefd8e | MIT | go.mod:graph-only | cache:github.com/niemeyer/pretty@v0.0.0-20200227124842-a10e7caefd8e/License | - |
| github.com/olekukonko/tablewriter | v0.0.5 | MIT | go.mod:graph-only | cache:github.com/olekukonko/tablewriter@v0.0.5/LICENSE.md | - |
| github.com/onsi/ginkgo/v2 | v2.32.0 | MIT | go.mod:graph-only | cache:github.com/onsi/ginkgo/v2@v2.32.0/LICENSE | - |
| github.com/onsi/gomega | v1.42.1 | MIT | go.mod:graph-only | cache:github.com/onsi/gomega@v1.42.1/LICENSE | - |
| github.com/opencontainers/go-digest | v1.0.0 | Apache-2.0 | go.mod:indirect | cache:github.com/opencontainers/go-digest@v1.0.0/LICENSE; cache:github.com/opencontainers/go-digest@v1.0.0/LICENSE.docs | documentation: CC-BY-SA-4.0 (not Go code) |
| github.com/opencontainers/image-spec | v1.1.1 | Apache-2.0 | go.mod:indirect | cache:github.com/opencontainers/image-spec@v1.1.1/LICENSE | - |
| github.com/pelletier/go-toml | v1.9.5 | Apache-2.0; MIT | go.mod:indirect | cache:github.com/pelletier/go-toml@v1.9.5/LICENSE | MIT generally; copied localtime files Apache-2.0 |
| github.com/peterbourgon/diskv | v2.0.1+incompatible | MIT | go.mod:indirect | cache:github.com/peterbourgon/diskv@v2.0.1+incompatible/LICENSE | - |
| github.com/pjbgf/sha1cd | v0.6.0 | Apache-2.0 | go.mod:graph-only | cache:github.com/pjbgf/sha1cd@v0.6.0/LICENSE | - |
| github.com/pkg/errors | v0.9.1 | BSD-2-Clause | go.mod:indirect | cache:github.com/pkg/errors@v0.9.1/LICENSE | - |
| github.com/pmezard/go-difflib | v1.0.0 | BSD-2-Clause | apps/sample-db/go.mod:graph-only | cache:github.com/pmezard/go-difflib@v1.0.0/LICENSE | - |
| github.com/pmezard/go-difflib | v1.0.1-0.20181226105442-5d4384ee4fb2 | BSD-2-Clause | go.mod:indirect | cache:github.com/pmezard/go-difflib@v1.0.1-0.20181226105442-5d4384ee4fb2/LICENSE | - |
| github.com/posener/complete | v1.2.3 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/posener/complete/v1.2.3/LICENSE.txt | - |
| github.com/poy/onpar | v1.1.2 | MIT | go.mod:graph-only | cache:github.com/poy/onpar@v1.1.2/LICENSE | - |
| github.com/pquerna/cachecontrol | v0.1.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/pquerna/cachecontrol/v0.1.0/LICENSE | - |
| github.com/prometheus/client_golang | v1.24.0 | Apache-2.0; BSD-3-Clause | go.mod:indirect | cache:github.com/prometheus/client_golang@v1.24.0/LICENSE; cache:github.com/prometheus/client_golang@v1.24.0/NOTICE | supplemental/bundled notices listed below |
| github.com/prometheus/client_golang | v1.25.0 | Apache-2.0; BSD-3-Clause | apps/sample-db/go.mod:direct | cache:github.com/prometheus/client_golang@v1.25.0/LICENSE; cache:github.com/prometheus/client_golang@v1.25.0/NOTICE | supplemental/bundled notices listed below |
| github.com/prometheus/client_model | v0.6.2 | Apache-2.0 | go.mod:indirect | cache:github.com/prometheus/client_model@v0.6.2/LICENSE; cache:github.com/prometheus/client_model@v0.6.2/NOTICE | - |
| github.com/prometheus/client_model | v0.6.3 | Apache-2.0 | apps/sample-db/go.mod:indirect | cache:github.com/prometheus/client_model@v0.6.3/LICENSE; cache:github.com/prometheus/client_model@v0.6.3/NOTICE | - |
| github.com/prometheus/common | v0.70.0 | Apache-2.0 | go.mod:indirect | cache:github.com/prometheus/common@v0.70.0/LICENSE; cache:github.com/prometheus/common@v0.70.0/NOTICE | - |
| github.com/prometheus/common | v0.72.0 | Apache-2.0 | apps/sample-db/go.mod:indirect | cache:github.com/prometheus/common@v0.72.0/LICENSE; cache:github.com/prometheus/common@v0.72.0/NOTICE | - |
| github.com/prometheus/otlptranslator | v1.0.0 | Apache-2.0 | go.mod:graph-only | cache:github.com/prometheus/otlptranslator@v1.0.0/LICENSE | - |
| github.com/prometheus/procfs | v0.21.1 | Apache-2.0 | go.mod:indirect | cache:github.com/prometheus/procfs@v0.21.1/LICENSE; cache:github.com/prometheus/procfs@v0.21.1/NOTICE | - |
| github.com/prometheus/procfs | v0.22.0 | Apache-2.0 | apps/sample-db/go.mod:indirect | cache:github.com/prometheus/procfs@v0.22.0/LICENSE; cache:github.com/prometheus/procfs@v0.22.0/NOTICE | - |
| github.com/redis/go-redis/extra/rediscmd/v9 | v9.0.5 | BSD-2-Clause | go.mod:graph-only | cache:github.com/redis/go-redis/extra/rediscmd/v9@v9.0.5/LICENSE | - |
| github.com/redis/go-redis/extra/redisotel/v9 | v9.0.5 | BSD-2-Clause | go.mod:graph-only | cache:github.com/redis/go-redis/extra/redisotel/v9@v9.0.5/LICENSE | - |
| github.com/redis/go-redis/v9 | v9.7.3 | BSD-2-Clause | go.mod:graph-only | cache:github.com/redis/go-redis/v9@v9.7.3/LICENSE | - |
| github.com/rivo/uniseg | v0.4.7 | MIT | go.mod:indirect | cache:github.com/rivo/uniseg@v0.4.7/LICENSE.txt | - |
| github.com/rogpeppe/go-internal | v1.14.1 | BSD-3-Clause | go.mod:graph-only | cache:github.com/rogpeppe/go-internal@v1.14.1/LICENSE | - |
| github.com/rubenv/sql-migrate | v1.8.1 | MIT | go.mod:indirect | cache:github.com/rubenv/sql-migrate@v1.8.1/LICENSE | supplemental/bundled notices listed below |
| github.com/russross/blackfriday | v1.6.0 | BSD-2-Clause | go.mod:graph-only | https://raw.githubusercontent.com/russross/blackfriday/v1.6.0/LICENSE.txt | - |
| github.com/russross/blackfriday/v2 | v2.1.0 | BSD-2-Clause | go.mod:indirect | cache:github.com/russross/blackfriday/v2@v2.1.0/LICENSE.txt | - |
| github.com/sahilm/fuzzy | v0.1.1 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/sahilm/fuzzy/c48e322e2a8f19bbd962faf82eee4cf11dbf81ba/LICENSE | - |
| github.com/santhosh-tekuri/jsonschema/v5 | v5.3.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/santhosh-tekuri/jsonschema/16bce71af51f6a4a775f11e649a347a8803940d3/LICENSE | - |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | Apache-2.0 | go.mod:indirect | cache:github.com/santhosh-tekuri/jsonschema/v6@v6.0.3/LICENSE | - |
| github.com/sebdah/goldie | v1.0.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/sebdah/goldie/v1.0.0/LICENSE | - |
| github.com/sergi/go-diff | v1.4.0 | MIT | go.mod:graph-only | cache:github.com/sergi/go-diff@v1.4.0/LICENSE | - |
| github.com/shopspring/decimal | v1.4.0 | MIT | go.mod:indirect | cache:github.com/shopspring/decimal@v1.4.0/LICENSE | - |
| github.com/sirupsen/logrus | v1.9.4 | MIT | go.mod:graph-only | cache:github.com/sirupsen/logrus@v1.9.4/LICENSE | - |
| github.com/skeema/knownhosts | v1.3.1 | Apache-2.0 | go.mod:graph-only | cache:github.com/skeema/knownhosts@v1.3.1/LICENSE; cache:github.com/skeema/knownhosts@v1.3.1/NOTICE | - |
| github.com/soheilhy/cmux | v0.1.5 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/soheilhy/cmux/v0.1.5/LICENSE | - |
| github.com/spf13/cast | v1.7.0 | MIT | go.mod:indirect | cache:github.com/spf13/cast@v1.7.0/LICENSE | - |
| github.com/spf13/cobra | v1.10.2 | Apache-2.0 | go.mod:direct | cache:github.com/spf13/cobra@v1.10.2/LICENSE.txt | - |
| github.com/spf13/pflag | v1.0.10 | BSD-3-Clause | go.mod:direct | cache:github.com/spf13/pflag@v1.0.10/LICENSE | - |
| github.com/stretchr/objx | v0.1.0 | MIT | apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/stretchr/objx/v0.1.0/LICENSE | - |
| github.com/stretchr/objx | v0.5.3 | MIT | go.mod:graph-only | cache:github.com/stretchr/objx@v0.5.3/LICENSE | - |
| github.com/stretchr/testify | v1.12.1 | BSD-2-Clause; ISC; MIT | go.mod:graph-only, apps/sample-db/go.mod:graph-only | cache:github.com/stretchr/testify@v1.12.1/LICENSE | supplemental/bundled notices listed below |
| github.com/tetratelabs/wabin | v0.0.0-20230304001439-f6f874872834 | Apache-2.0 | go.mod:indirect | cache:github.com/tetratelabs/wabin@v0.0.0-20230304001439-f6f874872834/LICENSE | - |
| github.com/tetratelabs/wazero | v1.12.0 | Apache-2.0 | go.mod:indirect | cache:github.com/tetratelabs/wazero@v1.12.0/LICENSE; cache:github.com/tetratelabs/wazero@v1.12.0/NOTICE | - |
| github.com/tmc/grpc-websocket-proxy | v0.0.0-20220101234140-673ab2c3ae75 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/tmc/grpc-websocket-proxy/673ab2c3ae75/LICENSE | - |
| github.com/vmihailenco/msgpack/v5 | v5.3.5 | BSD-2-Clause | go.mod:graph-only | https://raw.githubusercontent.com/vmihailenco/msgpack/v5.3.5/LICENSE | - |
| github.com/vmihailenco/tagparser/v2 | v2.0.0 | BSD-2-Clause | go.mod:graph-only | https://raw.githubusercontent.com/vmihailenco/tagparser/v2.0.0/LICENSE | - |
| github.com/x448/float16 | v0.8.4 | MIT | go.mod:indirect | cache:github.com/x448/float16@v0.8.4/LICENSE | - |
| github.com/xanzy/ssh-agent | v0.3.3 | Apache-2.0 | go.mod:graph-only | cache:github.com/xanzy/ssh-agent@v0.3.3/LICENSE | - |
| github.com/xhit/go-str2duration/v2 | v2.1.0 | BSD-3-Clause | go.mod:graph-only, apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/xhit/go-str2duration/017325bfda0895fe02177e4ebb07962e58735bbf/LICENSE | - |
| github.com/xiang90/probing | v0.0.0-20221125231312-a49e3df8f510 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/xiang90/probing/a49e3df8f510ee8b42e68345ca4636dbb161bd0a/LICENSE | - |
| github.com/xlab/treeprint | v1.2.0 | MIT | go.mod:indirect | cache:github.com/xlab/treeprint@v1.2.0/LICENSE | - |
| github.com/xo/terminfo | v0.0.0-20220910002029-abceb7e1c41e | MIT | go.mod:indirect | cache:github.com/xo/terminfo@v0.0.0-20220910002029-abceb7e1c41e/LICENSE | - |
| github.com/zclconf/go-cty | v1.18.1 | MIT | go.mod:indirect | cache:github.com/zclconf/go-cty@v1.18.1/LICENSE | - |
| github.com/zclconf/go-cty-debug | v0.0.0-20191215020915-b22d67c1ba0b | MIT | go.mod:graph-only | https://raw.githubusercontent.com/zclconf/go-cty-debug/b22d67c1ba0b/LICENSE | - |
| go.etcd.io/bbolt | v1.5.0 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/etcd-io/bbolt/e7a8b2dd498494a3766ba24dd94d3509e5588485/LICENSE | - |
| go.etcd.io/etcd/api/v3 | v3.7.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/etcd-io/etcd/4d71f7c83a2cef992d57abd3284edfaf66c91a4a/api/LICENSE | - |
| go.etcd.io/etcd/client/pkg/v3 | v3.7.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/etcd-io/etcd/4d71f7c83a2cef992d57abd3284edfaf66c91a4a/client/pkg/LICENSE | - |
| go.etcd.io/etcd/client/v3 | v3.7.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/etcd-io/etcd/4d71f7c83a2cef992d57abd3284edfaf66c91a4a/LICENSE | - |
| go.etcd.io/etcd/pkg/v3 | v3.7.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/etcd-io/etcd/4d71f7c83a2cef992d57abd3284edfaf66c91a4a/pkg/LICENSE | - |
| go.etcd.io/etcd/server/v3 | v3.7.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/etcd-io/etcd/4d71f7c83a2cef992d57abd3284edfaf66c91a4a/server/LICENSE | - |
| go.etcd.io/raft/v3 | v3.7.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/etcd-io/raft/b867cf13f6bc0dae21204302df97bc2355c3af55/LICENSE | - |
| go.opentelemetry.io/auto/sdk | v1.2.1 | Apache-2.0 | go.mod:graph-only | cache:go.opentelemetry.io/auto/sdk@v1.2.1/LICENSE | - |
| go.opentelemetry.io/contrib/bridges/prometheus | v0.67.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/contrib/bridges/prometheus@v0.67.0/LICENSE | - |
| go.opentelemetry.io/contrib/exporters/autoexport | v0.67.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/contrib/exporters/autoexport@v0.67.0/LICENSE | - |
| go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc | v0.68.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/open-telemetry/opentelemetry-go-contrib/45977a4b9cf4a60effd1ee07367043f7e9bcae66/LICENSE | - |
| go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp | v0.69.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.69.0/LICENSE | - |
| go.opentelemetry.io/otel | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel@v1.45.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc | v0.21.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@v0.21.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp | v0.21.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp@v0.21.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc | v1.42.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@v1.42.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp | v1.43.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp@v1.43.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/otlp/otlptrace | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/otlp/otlptrace@v1.45.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@v1.45.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@v1.45.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/prometheus | v0.64.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/prometheus@v0.64.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/stdout/stdoutlog | v0.21.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/stdout/stdoutlog@v0.21.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/stdout/stdoutmetric | v1.42.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/stdout/stdoutmetric@v1.42.0/LICENSE | - |
| go.opentelemetry.io/otel/exporters/stdout/stdouttrace | v1.42.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/exporters/stdout/stdouttrace@v1.42.0/LICENSE | - |
| go.opentelemetry.io/otel/log | v0.21.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/log@v0.21.0/LICENSE | - |
| go.opentelemetry.io/otel/metric | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/metric@v1.45.0/LICENSE | - |
| go.opentelemetry.io/otel/sdk | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/sdk@v1.45.0/LICENSE | - |
| go.opentelemetry.io/otel/sdk/log | v0.21.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/sdk/log@v0.21.0/LICENSE | - |
| go.opentelemetry.io/otel/sdk/metric | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/sdk/metric@v1.45.0/LICENSE | - |
| go.opentelemetry.io/otel/trace | v1.45.0 | Apache-2.0; BSD-3-Clause | go.mod:graph-only | cache:go.opentelemetry.io/otel/trace@v1.45.0/LICENSE | - |
| go.opentelemetry.io/proto/otlp | v1.11.0 | Apache-2.0 | go.mod:indirect | cache:go.opentelemetry.io/proto/otlp@v1.11.0/LICENSE | - |
| go.uber.org/goleak | v1.3.0 | MIT | go.mod:graph-only, apps/sample-db/go.mod:graph-only | cache:go.uber.org/goleak@v1.3.0/LICENSE | - |
| go.uber.org/multierr | v1.11.0 | MIT | go.mod:graph-only | cache:go.uber.org/multierr@v1.11.0/LICENSE.txt | - |
| go.uber.org/zap | v1.27.1 | MIT | go.mod:graph-only | cache:go.uber.org/zap@v1.27.1/LICENSE | - |
| go.yaml.in/yaml/v2 | v2.4.4 | Apache-2.0; MIT | go.mod:indirect, apps/sample-db/go.mod:graph-only | cache:go.yaml.in/yaml/v2@v2.4.4/LICENSE; cache:go.yaml.in/yaml/v2@v2.4.4/LICENSE.libyaml; cache:go.yaml.in/yaml/v2@v2.4.4/NOTICE | MIT for libyaml-derived files; Apache-2.0 for remaining implementation |
| go.yaml.in/yaml/v3 | v3.0.5 | Apache-2.0; MIT | go.mod:direct, apps/sample-db/go.mod:graph-only | cache:go.yaml.in/yaml/v3@v3.0.5/LICENSE; cache:go.yaml.in/yaml/v3@v3.0.5/NOTICE | MIT for libyaml-derived files; Apache-2.0 for remaining implementation |
| golang.org/x/crypto | v0.58.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/crypto@v0.58.0/LICENSE | - |
| golang.org/x/exp | v0.0.0-20260410095643-746e56fc9e2f | BSD-3-Clause | go.mod:graph-only | cache:golang.org/x/exp@v0.0.0-20260410095643-746e56fc9e2f/LICENSE | - |
| golang.org/x/mod | v0.41.0 | BSD-3-Clause | go.mod:graph-only, apps/sample-db/go.mod:graph-only | cache:golang.org/x/mod@v0.41.0/LICENSE | - |
| golang.org/x/net | v0.59.0 | BSD-3-Clause | apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/golang/net/540d04cfe5028e2655754591a4d3e08c586809f2/LICENSE | - |
| golang.org/x/net | v0.60.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/net@v0.60.0/LICENSE | - |
| golang.org/x/oauth2 | v0.36.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/oauth2@v0.36.0/LICENSE | - |
| golang.org/x/oauth2 | v0.37.0 | BSD-3-Clause | apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/golang/oauth2/c624b89dadc3221560b7345c090bbe69e90808ee/LICENSE | - |
| golang.org/x/sync | v0.23.0 | BSD-3-Clause | apps/sample-db/go.mod:indirect | cache:golang.org/x/sync@v0.23.0/LICENSE | - |
| golang.org/x/sync | v0.24.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/sync@v0.24.0/LICENSE | - |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause | apps/sample-db/go.mod:indirect | cache:golang.org/x/sys@v0.48.0/LICENSE | - |
| golang.org/x/sys | v0.49.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/sys@v0.49.0/LICENSE | - |
| golang.org/x/term | v0.47.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/term@v0.47.0/LICENSE | - |
| golang.org/x/text | v0.42.0 | BSD-3-Clause | apps/sample-db/go.mod:indirect | cache:golang.org/x/text@v0.42.0/LICENSE | - |
| golang.org/x/text | v0.43.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/text@v0.43.0/LICENSE | - |
| golang.org/x/time | v0.15.0 | BSD-3-Clause | go.mod:indirect | cache:golang.org/x/time@v0.15.0/LICENSE | - |
| golang.org/x/tools | v0.49.0 | BSD-3-Clause | apps/sample-db/go.mod:graph-only | https://raw.githubusercontent.com/golang/tools/18332fec72972efbb8ab9881984fec2d8cfc2b58/LICENSE | - |
| golang.org/x/tools | v0.51.0 | BSD-3-Clause | go.mod:graph-only | cache:golang.org/x/tools@v0.51.0/LICENSE | - |
| golang.org/x/tools/go/expect | v0.1.0-deprecated | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/golang/tools/a5f2a1893145b1c54ec841e1c5cae7f9853e724a/LICENSE | - |
| golang.org/x/tools/go/packages/packagestest | v0.1.1-deprecated | BSD-3-Clause | go.mod:graph-only | https://raw.githubusercontent.com/golang/tools/9b68bc9966ee62cbe4704dea268ca17f0d2cd8a7/LICENSE | - |
| gomodules.xyz/jsonpatch/v2 | v2.4.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/gomodules/jsonpatch/17d7994fea15a033020ac51ee05b7dda0afda0fe/LICENSE | - |
| google.golang.org/genproto/googleapis/api | v0.0.0-20260803160001-6ac0973c030d | Apache-2.0 | go.mod:graph-only | cache:google.golang.org/genproto/googleapis/api@v0.0.0-20260803160001-6ac0973c030d/LICENSE | - |
| google.golang.org/genproto/googleapis/rpc | v0.0.0-20260803160001-6ac0973c030d | Apache-2.0 | go.mod:graph-only | cache:google.golang.org/genproto/googleapis/rpc@v0.0.0-20260803160001-6ac0973c030d/LICENSE | - |
| google.golang.org/grpc | v1.83.1 | Apache-2.0 | go.mod:graph-only | cache:google.golang.org/grpc@v1.83.1/LICENSE; cache:google.golang.org/grpc@v1.83.1/NOTICE.txt | - |
| google.golang.org/protobuf | v1.36.12 | BSD-3-Clause | go.mod:indirect, apps/sample-db/go.mod:indirect | cache:google.golang.org/protobuf@v1.36.12/LICENSE | - |
| gopkg.in/check.v1 | v1.0.0-20201130134442-10cb98267c6c | BSD-2-Clause | go.mod:graph-only, apps/sample-db/go.mod:graph-only | cache:gopkg.in/check.v1@v1.0.0-20201130134442-10cb98267c6c/LICENSE | - |
| gopkg.in/evanphx/json-patch.v4 | v4.13.0 | BSD-3-Clause | go.mod:indirect | cache:gopkg.in/evanphx/json-patch.v4@v4.13.0/LICENSE | - |
| gopkg.in/go-jose/go-jose.v2 | v2.6.3 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/go-jose/go-jose/0dd4dd541c665fb292d664f77604ba694726f298/LICENSE | - |
| gopkg.in/inf.v0 | v0.9.1 | BSD-3-Clause | go.mod:indirect | cache:gopkg.in/inf.v0@v0.9.1/LICENSE | - |
| gopkg.in/natefinch/lumberjack.v2 | v2.2.1 | MIT | go.mod:graph-only | https://raw.githubusercontent.com/natefinch/lumberjack/4cb27fcfbb0f35cb48c542c5ea80b7c1d18933d0/LICENSE | - |
| gopkg.in/warnings.v0 | v0.1.2 | BSD-2-Clause | go.mod:graph-only | cache:gopkg.in/warnings.v0@v0.1.2/LICENSE | - |
| gopkg.in/yaml.v2 | v2.4.0 | Apache-2.0; MIT | go.mod:graph-only | cache:gopkg.in/yaml.v2@v2.4.0/LICENSE; cache:gopkg.in/yaml.v2@v2.4.0/LICENSE.libyaml; cache:gopkg.in/yaml.v2@v2.4.0/NOTICE | MIT for libyaml-derived files; Apache-2.0 for remaining implementation |
| gopkg.in/yaml.v3 | v3.0.1 | Apache-2.0; MIT | go.mod:graph-only, apps/sample-db/go.mod:graph-only | cache:gopkg.in/yaml.v3@v3.0.1/LICENSE; cache:gopkg.in/yaml.v3@v3.0.1/NOTICE | MIT for libyaml-derived files; Apache-2.0 for remaining implementation |
| helm.sh/helm/v4 | v4.3.1 | Apache-2.0 | go.mod:direct | cache:helm.sh/helm/v4@v4.3.1/LICENSE | supplemental/bundled notices listed below |
| k8s.io/api | v0.37.1 | Apache-2.0 | go.mod:direct | cache:k8s.io/api@v0.37.1/LICENSE | - |
| k8s.io/apiextensions-apiserver | v0.37.1 | Apache-2.0 | go.mod:indirect | cache:k8s.io/apiextensions-apiserver@v0.37.1/LICENSE | - |
| k8s.io/apimachinery | v0.37.1 | Apache-2.0; BSD-3-Clause | go.mod:direct | cache:k8s.io/apimachinery@v0.37.1/LICENSE | supplemental/bundled notices listed below |
| k8s.io/apiserver | v0.37.1 | Apache-2.0 | go.mod:indirect | cache:k8s.io/apiserver@v0.37.1/LICENSE | - |
| k8s.io/cli-runtime | v0.37.1 | Apache-2.0 | go.mod:direct | cache:k8s.io/cli-runtime@v0.37.1/LICENSE | - |
| k8s.io/client-go | v0.37.1 | Apache-2.0; BSD-3-Clause; MIT | go.mod:direct | cache:k8s.io/client-go@v0.37.1/LICENSE | supplemental/bundled notices listed below |
| k8s.io/code-generator | v0.37.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/kubernetes/code-generator/b7ddad3048d289a57a0540a9011ff091038f3f25/LICENSE | - |
| k8s.io/component-base | v0.37.1 | Apache-2.0 | go.mod:indirect | cache:k8s.io/component-base@v0.37.1/LICENSE | - |
| k8s.io/component-helpers | v0.37.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/kubernetes/component-helpers/cc6337f0b0340caa9afefba4e4a479376f46de86/LICENSE | - |
| k8s.io/gengo/v2 | v2.0.0-20260408192533-25e2208e0dc3 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/kubernetes/gengo/25e2208e0dc371a827289e7faced19a2dbcd480b/LICENSE | - |
| k8s.io/klog/v2 | v2.140.0 | Apache-2.0 | go.mod:direct | cache:k8s.io/klog/v2@v2.140.0/LICENSE | - |
| k8s.io/kms | v0.37.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/kubernetes/kms/7be9038b2e29ea3a03a005dd77e80f97dd75c10d/LICENSE | - |
| k8s.io/kube-openapi | v0.0.0-20260721132016-d427ff9ee9ad | Apache-2.0; BSD-3-Clause; MIT | go.mod:indirect | cache:k8s.io/kube-openapi@v0.0.0-20260721132016-d427ff9ee9ad/LICENSE | supplemental/bundled notices listed below |
| k8s.io/kubectl | v0.37.1 | Apache-2.0 | go.mod:indirect | cache:k8s.io/kubectl@v0.37.1/LICENSE | - |
| k8s.io/metrics | v0.37.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/kubernetes/metrics/0dbb5b986e22d45b8687347da28670e5ed6db1f3/LICENSE | - |
| k8s.io/streaming | v0.37.1 | Apache-2.0 | go.mod:indirect | cache:k8s.io/streaming@v0.37.1/LICENSE | - |
| k8s.io/utils | v0.0.0-20260626114624-be93311217bd | Apache-2.0; BSD-3-Clause | go.mod:indirect | cache:k8s.io/utils@v0.0.0-20260626114624-be93311217bd/LICENSE | supplemental/bundled notices listed below |
| oras.land/oras-go/v2 | v2.6.2 | Apache-2.0 | go.mod:indirect | cache:oras.land/oras-go/v2@v2.6.2/LICENSE | - |
| sigs.k8s.io/apiserver-network-proxy/konnectivity-client | v0.36.0 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/kubernetes-sigs/apiserver-network-proxy/e8432610cab5a696fcbdb37969ac1aacb8bcf7f8/LICENSE | - |
| sigs.k8s.io/controller-runtime | v0.25.2 | Apache-2.0 | go.mod:indirect | cache:sigs.k8s.io/controller-runtime@v0.25.2/LICENSE | - |
| sigs.k8s.io/json | v0.0.0-20250730193827-2d320260d730 | Apache-2.0; BSD-3-Clause | go.mod:indirect | cache:sigs.k8s.io/json@v0.0.0-20250730193827-2d320260d730/LICENSE | - |
| sigs.k8s.io/kind | v0.33.0 | Apache-2.0; MIT | go.mod:direct | cache:sigs.k8s.io/kind@v0.33.0/LICENSE | logo separately Apache-2.0 OR CC-BY-4.0; not counted as Go-code licence; supplemental/bundled notices listed below |
| sigs.k8s.io/kustomize/api | v0.21.1 | Apache-2.0 | go.mod:direct | cache:sigs.k8s.io/kustomize/api@v0.21.1/LICENSE | - |
| sigs.k8s.io/kustomize/kustomize/v5 | v5.8.1 | Apache-2.0 | go.mod:graph-only | https://raw.githubusercontent.com/kubernetes-sigs/kustomize/9790a1c3efd2fd35f1b768d495112834176581c1/LICENSE | - |
| sigs.k8s.io/kustomize/kyaml | v0.21.2 | Apache-2.0 | go.mod:direct | cache:sigs.k8s.io/kustomize/kyaml@v0.21.2/LICENSE | - |
| sigs.k8s.io/randfill | v1.0.0 | Apache-2.0 | go.mod:indirect | cache:sigs.k8s.io/randfill@v1.0.0/LICENSE; cache:sigs.k8s.io/randfill@v1.0.0/NOTICE | retain NOTICE recording third-party contribution provenance; names not reproduced here |
| sigs.k8s.io/structured-merge-diff/v6 | v6.4.2 | Apache-2.0 | go.mod:indirect | cache:sigs.k8s.io/structured-merge-diff/v6@v6.4.2/LICENSE | - |
| sigs.k8s.io/yaml | v1.6.0 | Apache-2.0; BSD-3-Clause; MIT | go.mod:direct | cache:sigs.k8s.io/yaml@v1.6.0/LICENSE | - |

### Supplemental Go notices

```text
These are separate from the module-wide root licence. Vendored/copied implementation licences are included
in the table's SPDX set; docs/assets and placeholder fixtures are explicitly qualified.
github.com/BurntSushi/toml v1.6.0 | cache:github.com/!burnt!sushi/toml@v1.6.0/cmd/toml-test-decoder/COPYING | MIT
github.com/BurntSushi/toml v1.6.0 | cache:github.com/!burnt!sushi/toml@v1.6.0/cmd/toml-test-encoder/COPYING | MIT
github.com/BurntSushi/toml v1.6.0 | cache:github.com/!burnt!sushi/toml@v1.6.0/cmd/tomlv/COPYING | MIT
github.com/aws/aws-sdk-go-v2 v1.47.6 | cache:github.com/aws/aws-sdk-go-v2@v1.47.6/internal/sync/singleflight/LICENSE | BSD-3-Clause
github.com/aws/smithy-go v1.28.8 | cache:github.com/aws/smithy-go@v1.28.8/internal/sync/singleflight/LICENSE | BSD-3-Clause
github.com/aws/smithy-go v1.28.8 | cache:github.com/aws/smithy-go@v1.28.8/transport/http/protocol/internal/json/internal/stdlib/LICENSE | BSD-3-Clause
github.com/cloudflare/circl v1.6.3 | cache:github.com/cloudflare/circl@v1.6.3/ecc/p384/LICENSE | BSD-3-Clause
github.com/distribution/distribution/v3 v3.1.1 | cache:github.com/distribution/distribution/v3@v3.1.1/docs/themes/hugo-geekdoc/LICENSE | MIT
github.com/go-jose/go-jose/v4 v4.1.4 | cache:github.com/go-jose/go-jose/v4@v4.1.4/json/LICENSE | BSD-3-Clause
github.com/go-openapi/testify/v2 v2.6.0 | cache:github.com/go-openapi/testify/v2@v2.6.0/docs/doc-site/project/LICENSE.md | Apache-2.0
github.com/go-openapi/testify/v2 v2.6.0 | cache:github.com/go-openapi/testify/v2@v2.6.0/docs/doc-site/project/NOTICE.md | Apache-2.0, BSD-2-Clause, ISC, MIT
github.com/go-openapi/testify/v2 v2.6.0 | cache:github.com/go-openapi/testify/v2@v2.6.0/internal/difflib/LICENSE | Apache-2.0
github.com/go-openapi/testify/v2 v2.6.0 | cache:github.com/go-openapi/testify/v2@v2.6.0/internal/difflib/NOTICE | Apache-2.0, BSD-2-Clause
github.com/go-openapi/testify/v2 v2.6.0 | cache:github.com/go-openapi/testify/v2@v2.6.0/internal/spew/LICENSE | ISC
github.com/google/pprof v0.0.0-20260402051712-545e8a4df936 | cache:github.com/google/pprof@v0.0.0-20260402051712-545e8a4df936/third_party/svgpan/LICENSE | BSD-3-Clause
github.com/gosuri/uitable v0.0.4 | cache:github.com/gosuri/uitable@v0.0.4/util/wordwrap/LICENSE.md | MIT
github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 | cache:github.com/grpc-ecosystem/grpc-gateway/v2@v2.29.0/internal/casing/LICENSE.md | BSD-3-Clause
github.com/hashicorp/golang-lru/v2 v2.0.5 | cache:github.com/hashicorp/golang-lru/v2@v2.0.5/simplelru/LICENSE_list | BSD-3-Clause
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/gzhttp/LICENSE | Apache-2.0
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/internal/lz4ref/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/internal/snapref/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/s2/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/s2/cmd/internal/filepathx/LICENSE | MIT
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/s2/cmd/internal/readahead/LICENSE | MIT
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/snappy/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/snappy/xerial/LICENSE | MIT
github.com/klauspost/compress v1.19.0 | cache:github.com/klauspost/compress@v1.19.0/zstd/internal/xxhash/LICENSE.txt | MIT
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/gzhttp/LICENSE | Apache-2.0
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/internal/lz4ref/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/internal/snapref/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/s2/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/s2/cmd/internal/filepathx/LICENSE | MIT
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/s2/cmd/internal/readahead/LICENSE | MIT
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/snappy/LICENSE | BSD-3-Clause
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/snappy/xerial/LICENSE | MIT
github.com/klauspost/compress v1.20.1 | cache:github.com/klauspost/compress@v1.20.1/zstd/internal/xxhash/LICENSE.txt | MIT
github.com/moby/spdystream v0.5.1 | cache:github.com/moby/spdystream@v0.5.1/spdy/LICENSE | BSD-3-Clause
github.com/prometheus/client_golang v1.24.0 | cache:github.com/prometheus/client_golang@v1.24.0/internal/github.com/golang/gddo/LICENSE | BSD-3-Clause
github.com/prometheus/client_golang v1.25.0 | cache:github.com/prometheus/client_golang@v1.25.0/internal/github.com/golang/gddo/LICENSE | BSD-3-Clause
github.com/rubenv/sql-migrate v1.8.1 | cache:github.com/rubenv/sql-migrate@v1.8.1/sqlparse/LICENSE | MIT
github.com/stretchr/testify v1.12.1 | cache:github.com/stretchr/testify@v1.12.1/internal/difflib/LICENSE | BSD-2-Clause
github.com/stretchr/testify v1.12.1 | cache:github.com/stretchr/testify@v1.12.1/internal/spew/LICENSE | ISC
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/loader/testdata/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/loader/testdata/frobnitz.v3.reqs/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/loader/testdata/frobnitz/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/loader/testdata/frobnitz_backslash/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/loader/testdata/frobnitz_with_bom/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/loader/testdata/frobnitz_with_dev_null/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/util/testdata/dependent-chart-alias/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/util/testdata/dependent-chart-no-requirements-yaml/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/util/testdata/dependent-chart-with-all-in-requirements-yaml/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/util/testdata/dependent-chart-with-mixed-requirements-yaml/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/internal/chart/v3/util/testdata/frobnitz/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/loader/testdata/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/loader/testdata/frobnitz.v1/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/loader/testdata/frobnitz.v2.reqs/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/loader/testdata/frobnitz/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/loader/testdata/frobnitz_backslash/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/loader/testdata/frobnitz_with_bom/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/loader/testdata/frobnitz_with_dev_null/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/util/testdata/dependent-chart-alias/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/util/testdata/dependent-chart-no-requirements-yaml/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/util/testdata/dependent-chart-with-all-in-requirements-yaml/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/util/testdata/dependent-chart-with-mixed-requirements-yaml/LICENSE | test fixture placeholder, not a licence grant
helm.sh/helm/v4 v4.3.1 | cache:helm.sh/helm/v4@v4.3.1/pkg/chart/v2/util/testdata/frobnitz/LICENSE | test fixture placeholder, not a licence grant
k8s.io/apimachinery v0.37.1 | cache:k8s.io/apimachinery@v0.37.1/third_party/forked/golang/LICENSE | BSD-3-Clause
k8s.io/client-go v0.37.1 | cache:k8s.io/client-go@v0.37.1/third_party/forked/golang/LICENSE | BSD-3-Clause
k8s.io/client-go v0.37.1 | cache:k8s.io/client-go@v0.37.1/third_party/forked/httpcache/LICENSE | MIT
k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad | cache:k8s.io/kube-openapi@v0.0.0-20260721132016-d427ff9ee9ad/pkg/internal/third_party/go-json-experiment/json/LICENSE | BSD-3-Clause
k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad | cache:k8s.io/kube-openapi@v0.0.0-20260721132016-d427ff9ee9ad/pkg/internal/third_party/govalidator/LICENSE | MIT
k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad | cache:k8s.io/kube-openapi@v0.0.0-20260721132016-d427ff9ee9ad/pkg/validation/errors/LICENSE | Apache-2.0
k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad | cache:k8s.io/kube-openapi@v0.0.0-20260721132016-d427ff9ee9ad/pkg/validation/spec/LICENSE | Apache-2.0
k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad | cache:k8s.io/kube-openapi@v0.0.0-20260721132016-d427ff9ee9ad/pkg/validation/strfmt/LICENSE | Apache-2.0
k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad | cache:k8s.io/kube-openapi@v0.0.0-20260721132016-d427ff9ee9ad/pkg/validation/validate/LICENSE | Apache-2.0
k8s.io/utils v0.0.0-20260626114624-be93311217bd | cache:k8s.io/utils@v0.0.0-20260626114624-be93311217bd/inotify/LICENSE | BSD-3-Clause
k8s.io/utils v0.0.0-20260626114624-be93311217bd | cache:k8s.io/utils@v0.0.0-20260626114624-be93311217bd/internal/third_party/forked/golang/LICENSE | BSD-3-Clause
k8s.io/utils v0.0.0-20260626114624-be93311217bd | cache:k8s.io/utils@v0.0.0-20260626114624-be93311217bd/third_party/forked/golang/LICENSE | BSD-3-Clause
k8s.io/utils v0.0.0-20260626114624-be93311217bd | cache:k8s.io/utils@v0.0.0-20260626114624-be93311217bd/third_party/forked/golang/btree/LICENSE | Apache-2.0
sigs.k8s.io/kind v0.33.0 | cache:sigs.k8s.io/kind@v0.33.0/hack/third_party/gimme/LICENSE | MIT
sigs.k8s.io/kind v0.33.0 | cache:sigs.k8s.io/kind@v0.33.0/images/base/scripts/third_party/gimme/LICENSE | MIT
sigs.k8s.io/kind v0.33.0 | cache:sigs.k8s.io/kind@v0.33.0/images/local-path-provisioner/scripts/third_party/gimme/LICENSE | MIT
sigs.k8s.io/kind v0.33.0 | cache:sigs.k8s.io/kind@v0.33.0/logo/LICENSE | logo only: Apache-2.0 OR CC-BY-4.0

Unverified module rows: none
```
