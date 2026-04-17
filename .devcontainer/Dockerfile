FROM golang:1.22-bookworm

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
ARG CONTROLLER_GEN_VERSION=v0.14.0
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

# Non-root user matching distroless UID (avoids permission issues with bind-mounts)
RUN useradd -u 65532 -m -s /bin/bash vscode 2>/dev/null || true
USER vscode
WORKDIR /workspace
