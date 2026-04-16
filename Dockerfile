# First stage: build the Go binary
FROM golang:1.23-alpine AS builder

# Build-time argument for network (colosseum, garden, orchard, lighthouse, local)
ARG NETWORK=colosseum

# Install make and other build dependencies
RUN apk --no-cache add make gcc musl-dev
# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Only modify TimeToStartTx for local network (allows immediate transactions)
RUN if [ "$NETWORK" = "local" ]; then \
        sed -i 's/TimeToStartTx[[:space:]]*uint64[[:space:]]*=[[:space:]]*15 \* BlocksPerDay/TimeToStartTx uint64 = 0/' params/protocol_params.go && \
        echo "Modified TimeToStartTx for local network:" && \
        grep "TimeToStartTx" params/protocol_params.go; \
    fi

# Build the Go app using the Makefile
RUN make go-quai

# Second stage: create a smaller runtime image
FROM alpine:latest

# Install any dependencies required to run the application
RUN apk update && apk add --no-cache \
    coreutils \
    findutils \
    procps \
    util-linux \
    curl \
    ca-certificates

# Set the Current Working Directory inside the container
WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/build/bin/go-quai ./build/bin/go-quai
COPY --from=builder /app/VERSION ./VERSION
COPY --from=builder /app/params/genesis_alloc.json ./params/genesis_alloc.json

# Ensure the binary has execute permissions
RUN chmod +x ./build/bin/go-quai

# Configurable environment (colosseum, garden, orchard, lighthouse, local)
ENV QUAI_ENVIRONMENT=colosseum
ENV QUAI_LOG_LEVEL=info

# Expose the necessary ports
EXPOSE 8001 8002 8003 8004 8200 8201 8202 8220 8221 8222 8240 8241 8242 9001 9002 9003 9004 9200 9201 9202 9220 9221 9222 9240 9241 9242

# Command to run the executable
# - Bind RPC to 0.0.0.0 so it's accessible from outside the container
# - Set log levels via environment variable
CMD ["sh", "-c", "./build/bin/go-quai start --node.environment ${QUAI_ENVIRONMENT} --rpc.http-addr 0.0.0.0 --rpc.ws-addr 0.0.0.0 --node.log-level ${QUAI_LOG_LEVEL} --peers.log-level ${QUAI_LOG_LEVEL}"]
