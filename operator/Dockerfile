# Pinned by digest for reproducible builds and supply-chain hardening.
# Tag/digest pair as of resolution: golang:1.22.12 (the current tip of the
# 1.22 line). Bump via Dependabot's docker ecosystem (see .github/dependabot.yml).
FROM golang:1.26.3@sha256:313faae491b410a35402c05d35e7518ae99103d957308e940e1ae2cfa0aac29b AS builder
WORKDIR /workspace

COPY go.mod go.sum* ./
RUN go mod download

COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o manager main.go

# Pinned by digest. The `nonroot` tag floats; Dependabot will surface bumps.
FROM gcr.io/distroless/static:nonroot@sha256:e3f945647ffb95b5839c07038d64f9811adf17308b9121d8a2b87b6a22a80a39
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
