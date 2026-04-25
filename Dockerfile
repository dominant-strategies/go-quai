# First stage: build the Go binary
FROM golang:1.23-alpine AS builder

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
COPY --from=builder /app/params/forfeiture_addresses.json ./params/forfeiture_addresses.json


# Ensure the binary has execute permissions
RUN chmod +x ./build/bin/go-quai

# Expose the necessary ports
EXPOSE 4002 8001 8002 8200 9001 9002 9200

# Command to run the executable
CMD ["./build/bin/go-quai", "start"]
