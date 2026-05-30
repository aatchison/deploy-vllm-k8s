# vLLM operator build + deploy

# Local image tag (used for microk8s image-load).
# CI publishes ghcr.io/aatchison/vllm-operator with a short-SHA tag (no :latest;
# see issue #64). Override the tag to publish locally if needed:
#   IMG=ghcr.io/aatchison/vllm-operator:$(git rev-parse --short HEAD) make docker-build
IMG ?= docker.io/library/vllm-operator:local
KUBECTL ?= microk8s kubectl
NAMESPACE ?= vllm-system
INSTANCE ?= 31b-96
MODEL_NAMESPACE ?= vllm

# Let the Go toolchain auto-download the version pinned in go.mod (issue #112).
# Without this, hosts whose system Go is older than the go.mod `go` directive
# fail every target that invokes go/controller-gen — including `make deploy`
# via #110's CRD-reapply prerequisite chain. `?=` keeps the override path
# open for maintainers who deliberately want a strict-local toolchain.
# `export` ensures controller-gen's internal `go list` subprocess inherits it.
export GOTOOLCHAIN ?= auto

LOCALBIN := $(CURDIR)/bin
CONTROLLER_GEN := $(LOCALBIN)/controller-gen
KUSTOMIZE := $(LOCALBIN)/kustomize

CONTROLLER_GEN_VERSION ?= v0.20.1
KUSTOMIZE_VERSION ?= v5.3.0

.PHONY: all
all: build

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

# If controller-gen is already on PATH (e.g. inside the devcontainer), create a
# symlink so the $(CONTROLLER_GEN) prerequisite resolves without re-downloading.
$(CONTROLLER_GEN): | $(LOCALBIN)
	@if command -v controller-gen >/dev/null 2>&1; then \
	  ln -sf "$$(command -v controller-gen)" $(CONTROLLER_GEN); \
	else \
	  GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION); \
	fi

$(KUSTOMIZE): | $(LOCALBIN)
	@if command -v kustomize >/dev/null 2>&1; then \
	  ln -sf "$$(command -v kustomize)" $(KUSTOMIZE); \
	else \
	  GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION); \
	fi

.PHONY: generate
generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: manifests
manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) crd rbac:roleName=vllm-operator-role webhook paths="./..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: ci
ci: generate fmt vet lint test

.PHONY: build
build: generate fmt vet
	go build -o bin/manager main.go

.PHONY: test
test: generate fmt vet
	go test ./... -coverprofile cover.out

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: image-load
image-load: docker-build
	docker save $(IMG) -o bin/vllm-operator.tar
	microk8s ctr image import bin/vllm-operator.tar
	rm -f bin/vllm-operator.tar

.PHONY: install
install: manifests
	$(KUBECTL) apply -f config/crd/bases

.PHONY: uninstall
uninstall:
	$(KUBECTL) delete --ignore-not-found -f config/crd/bases

.PHONY: deploy
# CRDs are reapplied before manager.yaml so the cluster's OpenAPI schema is
# always at least as current as the operator binary being rolled out. Without
# this dependency, a re-pin to a build that added new CRD fields (e.g. #93's
# spec.serviceType / spec.apiKey) silently leaves stripped fields on user CRs
# because kubectl warns on unknown fields but does not fail (issue #110).
deploy: manifests install
	$(KUBECTL) apply -f config/rbac
	$(KUBECTL) apply -f config/manager/manager.yaml
	$(KUBECTL) apply -f config/manager/service.yaml
	# Operator-namespace NetworkPolicies (issue #142). Unlike the workload
	# templates one directory up (which target the `vllm` namespace and assume a
	# specific ingress controller, so they stay opt-in via deploy-networkpolicy),
	# config/networkpolicy/operator/ is self-contained: default-deny ingress for
	# vllm-system plus a metrics-scrape allow and a DNS+apiserver egress list. No
	# per-cluster edits are needed, so it ships in the standard deploy path and
	# the operator namespace is never left unprotected. On a CNI that does not
	# enforce NetworkPolicy these are a harmless no-op (issue #75). Applied after
	# manager.yaml so the vllm-system namespace already exists.
	$(KUBECTL) apply -f config/networkpolicy/operator/

.PHONY: undeploy
undeploy:
	$(KUBECTL) delete --ignore-not-found -f config/networkpolicy/operator/
	$(KUBECTL) delete --ignore-not-found -f config/manager/service.yaml
	$(KUBECTL) delete --ignore-not-found -f config/manager/manager.yaml
	$(KUBECTL) delete --ignore-not-found -f config/rbac

# Optional: apply the ServiceMonitors and Grafana dashboard for Prometheus
# Operator scraping. Kept separate from `make deploy` because the
# monitoring.coreos.com CRDs may not be installed on every cluster (issue
# #40), and the grafana sidecar lookup label / namespace are stack-specific.
#
# config/prometheus/ contains three ServiceMonitors:
#   - monitor.yaml         (the operator's own controller-runtime metrics)
#   - dcgm-exporter.yaml   (NVIDIA DCGM, in gpu-operator-resources ns)
#   - vllm-instances.yaml  (vLLM /metrics on every operator-managed Service)
#
# The Grafana dashboard is committed as JSON in config/grafana/ and wrapped
# into a ConfigMap at apply time (avoids keeping a 200-line YAML literal
# block in sync with the JSON). The label/annotation match the kiwigrid
# sidecar (kube-prometheus-stack default): grafana_dashboard=1 picks the
# ConfigMap up, grafana_folder controls the folder name.
GRAFANA_NAMESPACE ?= monitoring
.PHONY: deploy-monitoring
deploy-monitoring:
	$(KUBECTL) apply -f config/prometheus/
	$(KUBECTL) create configmap vllm-grafana-dashboard \
	  --namespace $(GRAFANA_NAMESPACE) \
	  --from-file=config/grafana/vllm-dashboard.json \
	  --dry-run=client -o yaml \
	| $(KUBECTL) label --local --dry-run=client -o yaml -f - grafana_dashboard=1 \
	| $(KUBECTL) annotate --local --dry-run=client -o yaml -f - grafana_folder=vLLM \
	| $(KUBECTL) apply -f -

.PHONY: undeploy-monitoring
undeploy-monitoring:
	$(KUBECTL) delete --ignore-not-found -n $(GRAFANA_NAMESPACE) configmap vllm-grafana-dashboard
	$(KUBECTL) delete --ignore-not-found -f config/prometheus/

# Issue #75: the *workload* NetworkPolicy templates are kept out of `make deploy`
# because they require a CNI that enforces NetworkPolicy (Calico, Cilium,
# kube-router, Antrea, etc.) and must be customised per cluster before applying.
# On a CNI without NP support the manifests apply but nothing enforces them —
# silently insecure. Apply explicitly only after confirming the cluster's CNI
# enforces ingress/egress rules.
#
# Customise the templates (especially allow-from-ingress-controller.yaml's
# namespaceSelector) before applying — the defaults assume nginx-ingress in
# the `ingress-nginx` namespace.
#
# Scope note: this target applies only the workload templates in the top of
# config/networkpolicy/ (kubectl -f is non-recursive, so config/networkpolicy/
# operator/ is not included here). The operator-namespace policies ship with
# `make deploy` instead — see that target.
.PHONY: deploy-networkpolicy
deploy-networkpolicy:
	$(KUBECTL) apply -f config/networkpolicy/

.PHONY: undeploy-networkpolicy
undeploy-networkpolicy:
	$(KUBECTL) delete --ignore-not-found -f config/networkpolicy/

.PHONY: mig-setup
mig-setup:
	$(KUBECTL) apply -f config/mig-setup/serviceaccount.yaml
	$(KUBECTL) apply -f config/mig-setup/clusterrole.yaml
	$(KUBECTL) apply -f config/mig-setup/clusterrolebinding.yaml
	$(KUBECTL) apply -f config/mig-setup/role.yaml
	$(KUBECTL) apply -f config/mig-setup/rolebinding.yaml
	$(KUBECTL) apply -f config/mig-setup/configmap.yaml
	$(KUBECTL) delete job mig-setup -n kube-system --ignore-not-found
	$(KUBECTL) apply -f config/mig-setup/job.yaml

.PHONY: apply-samples
apply-samples:
	$(KUBECTL) apply -f config/samples/presets/
	$(KUBECTL) apply -f config/samples/instances/

# Run a quick health-check against a live VLLMInstance.
# Override defaults: make smoke-test INSTANCE=e2b MODEL_NAMESPACE=vllm
.PHONY: smoke-test
smoke-test:
	bash hack/smoke-test.sh $(INSTANCE) $(MODEL_NAMESPACE) "$(KUBECTL)"
