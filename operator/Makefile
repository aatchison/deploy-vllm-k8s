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
deploy: manifests
	$(KUBECTL) apply -f config/rbac
	$(KUBECTL) apply -f config/manager/manager.yaml
	$(KUBECTL) apply -f config/manager/service.yaml

.PHONY: undeploy
undeploy:
	$(KUBECTL) delete --ignore-not-found -f config/manager/service.yaml
	$(KUBECTL) delete --ignore-not-found -f config/manager/manager.yaml
	$(KUBECTL) delete --ignore-not-found -f config/rbac

# Optional: apply the ServiceMonitor for Prometheus Operator scraping.
# Kept separate from `make deploy` because the monitoring.coreos.com CRDs
# may not be installed on every cluster (issue #40).
.PHONY: deploy-monitoring
deploy-monitoring:
	$(KUBECTL) apply -f config/prometheus/monitor.yaml

.PHONY: undeploy-monitoring
undeploy-monitoring:
	$(KUBECTL) delete --ignore-not-found -f config/prometheus/monitor.yaml

# Issue #75: NetworkPolicy templates are kept out of `make deploy` because
# they require a CNI that enforces NetworkPolicy (Calico, Cilium, kube-router,
# Antrea, etc.). On a CNI without NP support the manifests apply but nothing
# enforces them — silently insecure. Apply explicitly only after confirming
# the cluster's CNI enforces ingress/egress rules.
#
# Customise the templates (especially allow-from-ingress-controller.yaml's
# namespaceSelector) before applying — the defaults assume nginx-ingress in
# the `ingress-nginx` namespace.
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
