FROM golang:1.25-alpine AS builder


WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the provider
RUN CGO_ENABLED=0 GOOS=linux go build -o terraform-provider-fabricapi

# Final stage
FROM hashicorp/terraform:1.6

# Create plugin directory
RUN mkdir -p /root/.terraform.d/plugins/registry.terraform.io/local/fabricapi/1.0.0/linux_amd64

# Copy the provider binary
COPY --from=builder /build/terraform-provider-fabricapi \
    /root/.terraform.d/plugins/registry.terraform.io/local/fabricapi/1.0.0/linux_amd64/

# Set working directory
WORKDIR /workspace

# Copy main terraform files (excluding .example files to avoid duplicates)
COPY examples/ /workspace/



# Override entrypoint and default to shell
ENTRYPOINT ["/bin/sh"]
CMD ["-l"]