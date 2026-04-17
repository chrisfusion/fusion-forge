# SPDX-License-Identifier: GPL-3.0-or-later
# Builder image for fusion-forge CI builds.
# Multi-stage: compile the Go uploader binary, then run inside python:3.12-slim.

# Stage 1: compile the Go uploader binary.
FROM golang:1.25-alpine AS go-builder

WORKDIR /src
COPY main.go .

# Initialise a throw-away module so we can build without the main module.
RUN go mod init forge-builder && \
    CGO_ENABLED=0 GOOS=linux go build -a -o /forge-builder .

# Stage 2: runtime image based on Python 3.12-slim.
FROM python:3.12-slim-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go-builder /forge-builder /usr/local/bin/forge-builder

WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/forge-builder"]
