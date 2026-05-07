# controller-tools v0.20.1 requires go >= 1.25; CI installs that via
# actions/setup-go's auto-toolchain, so the devcontainer needs a matching
# base to keep `make generate` reproducible locally.
FROM golang:1.25-bookworm

# System deps: kubectl + tools needed for make targets
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        curl \
        git \
        jq \
        make \
        unzip \
    && rm -rf /var/lib/apt/lists/*

# kubectl
ARG KUBECTL_VERSION=v1.29.3
RUN curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
        -o /usr/local/bin/kubectl \
    && chmod +x /usr/local/bin/kubectl

# controller-gen — pinned to match operator/Makefile
ARG CONTROLLER_GEN_VERSION=v0.20.1
RUN GOBIN=/usr/local/bin go install \
        sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}

# kustomize — pinned to match operator/Makefile
ARG KUSTOMIZE_VERSION=v5.3.0
RUN GOBIN=/usr/local/bin go install \
        sigs.k8s.io/kustomize/kustomize/v5@${KUSTOMIZE_VERSION}

# golangci-lint
ARG GOLANGCI_LINT_VERSION=v1.57.2
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
        | sh -s -- -b /usr/local/bin ${GOLANGCI_LINT_VERSION}

# kubeconform — same binary the PR-validation workflow uses, so YAML/CRD
# checks can be reproduced inside the devcontainer with the exact CI flags.
ARG KUBECONFORM_VERSION=v0.6.4
RUN curl -sSfL \
        "https://github.com/yannh/kubeconform/releases/download/${KUBECONFORM_VERSION}/kubeconform-linux-amd64.tar.gz" \
        | tar -xz -C /usr/local/bin kubeconform \
    && chmod +x /usr/local/bin/kubeconform

# Non-root user matching distroless UID (avoids permission issues with bind-mounts).
# /go is root-owned after the `go install` steps above. The
# docker-outside-of-docker devcontainer feature overrides the in-image user
# at runtime (typically UID 1000), so a chown to a specific UID isn't enough;
# make /go writable for any UID so `go mod download` works regardless of the
# remapped user.
RUN useradd -u 65532 -m -s /bin/bash vscode 2>/dev/null || true \
    && chmod -R a+rwX /go
USER vscode
WORKDIR /workspace
