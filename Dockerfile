# First stage: build the Go binary
FROM golang:1.21-alpine AS builder

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
RUN apk --no-cache add ca-certificates


# Set the Current Working Directory inside the container
WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/build/bin/go-quai ./build/bin/go-quai
COPY --from=builder /app/VERSION ./VERSION


# Ensure the binary has execute permissions
RUN chmod +x ./build/bin/go-quai

# Expose the necessary ports
EXPOSE 8001 8002 8003 8004 8200 8201 8202 8220 8221 8222 8240 8241 8242 9001 9002 9003 9004 9200 9201 9202 9220 9221 9222 9240 9241 9242

# Command to run the executable
CMD ["./build/bin/go-quai", "start"]
