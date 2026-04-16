# SPDX-License-Identifier: GPL-3.0-or-later
# Build stage: compiles both the REST server and the operator binary.
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY api/    api/
COPY cmd/    cmd/
COPY internal/ internal/
COPY migrations/ migrations/

RUN CGO_ENABLED=0 GOOS=linux go build -a -o server  ./cmd/server/
RUN CGO_ENABLED=0 GOOS=linux go build -a -o operator ./cmd/operator/

# Runtime stage: minimal distroless image.
FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /workspace/server   .
COPY --from=builder /workspace/operator .
# Embed migrations so the server binary can run them at startup.
COPY --from=builder /workspace/migrations migrations/

USER 65532:65532

# Default entrypoint is the REST server.
# Override with /operator for the operator Deployment.
ENTRYPOINT ["/server"]
