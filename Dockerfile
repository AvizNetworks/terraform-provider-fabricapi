# syntax=docker/dockerfile:1
# BuildKit caches speed up repeat builds (use: DOCKER_BUILDKIT=1).

FROM golang:1.25-alpine AS builder

WORKDIR /build

RUN apk add --no-cache git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

ARG CACHEBUST=1
ARG PROVIDER_VERSION=1.0.0

RUN echo "$CACHEBUST" >/dev/null && go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build -o "terraform-provider-fabricapi_v${PROVIDER_VERSION}" .

FROM hashicorp/terraform:1.6

ARG PROVIDER_VERSION=1.0.0

RUN mkdir -p "/root/.terraform.d/plugins/registry.terraform.io/local/fabricapi/${PROVIDER_VERSION}/linux_amd64"

COPY --from=builder "/build/terraform-provider-fabricapi_v${PROVIDER_VERSION}" \
  "/root/.terraform.d/plugins/registry.terraform.io/local/fabricapi/${PROVIDER_VERSION}/linux_amd64/"

WORKDIR /workspace

# Copy examples for fully self-contained runs (no mounts).
COPY examples/ /workspace/

ENTRYPOINT ["/bin/sh"]
CMD ["-l"]

