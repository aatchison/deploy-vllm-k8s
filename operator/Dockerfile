# Pinned by digest for reproducible builds and supply-chain hardening.
# Tag/digest pair as of resolution: golang:1.26.8 (trivy flagged 11 HIGH stdlib
# CVEs in 1.26.3, all fixed <= 1.26.6). Bump via Dependabot's docker ecosystem.
FROM golang:1.26.8@sha256:3c3e25a4da13fd0478eed2df1eb35a0e667094a7124d3993a6a1d30f71c17e79 AS builder
WORKDIR /workspace

COPY go.mod go.sum* ./
RUN go mod download

COPY main.go cache_scope.go ./
COPY api/ api/
COPY controllers/ controllers/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o manager .

# Pinned by digest. The `nonroot` tag floats; Dependabot will surface bumps.
FROM gcr.io/distroless/static:nonroot@sha256:e3f945647ffb95b5839c07038d64f9811adf17308b9121d8a2b87b6a22a80a39
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
