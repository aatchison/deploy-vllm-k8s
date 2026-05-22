# Pinned by digest for reproducible builds and supply-chain hardening.
# Tag/digest pair as of resolution: golang:1.22.12 (the current tip of the
# 1.22 line). Bump via Dependabot's docker ecosystem (see .github/dependabot.yml).
FROM golang:1.26.3@sha256:efaccb5b497e90df3ebe5216cc25cd9f98e73874e2d638b56e38d4a3f098c41c AS builder
WORKDIR /workspace

COPY go.mod go.sum* ./
RUN go mod download

COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o manager main.go

# Pinned by digest. The `nonroot` tag floats; Dependabot will surface bumps.
FROM gcr.io/distroless/static:nonroot@sha256:963fa6c544fe5ce420f1f54fb88b6fb01479f054c8056d0f74cc2c6000df5240
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
