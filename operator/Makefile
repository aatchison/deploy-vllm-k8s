# vLLM operator build + deploy

# Local image tag (used for microk8s image-load).
# Override to publish: IMG=ghcr.io/aatchison/vllm-operator:latest make docker-build
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

.PHONY: undeploy
undeploy:
	$(KUBECTL) delete --ignore-not-found -f config/manager/manager.yaml
	$(KUBECTL) delete --ignore-not-found -f config/rbac

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
