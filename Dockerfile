# Support setting various labels on the final image
ARG COMMIT=""
ARG VERSION=""
ARG BUILDNUM=""

# Build Geth in a stock Go builder container
FROM golang:1.17-alpine as builder
RUN apk add --no-cache gcc musl-dev linux-headers git make

ADD . /go-quai

WORKDIR /go-quai

RUN go run build/ci.go install ./cmd/quai

RUN make go-quai

# Stage 2
FROM golang:1.17-alpine

EXPOSE 8546 8547 30303 30303/udp
EXPOSE 8578 8579 30304 30304/udp
EXPOSE 8580 8581 30305 30305/udp
EXPOSE 8582 8583 30306 30306/udp
EXPOSE 8610 8611 30307 30307/udp
EXPOSE 8542 8643 30308 30308/udp
EXPOSE 8674 8675 30309 30309/udp
EXPOSE 8512 8613 30310 30310/udp
EXPOSE 8544 8645 30311 30311/udp
EXPOSE 8576 8677 30312 30312/udp
EXPOSE 8614 8615 30313 30313/udp
EXPOSE 8646 8647 30314 30314/udp
EXPOSE 8678 8679 30315 30315/udp

COPY --from=builder /go-quai/build/bin ./build/bin

WORKDIR ./build/bin

CMD ./quai --$NETWORK --syncmode full --http --http.vhosts="*" --ws --http.addr 0.0.0.0 --http.api eth,net,web3,quai,txpool,debug --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai,txpool,debug --port $TCP_PORT --http.port $HTTP_PORT --ws.port $WS_PORT --ws.origins="*" --http.corsdomain="*" $REGION $ZONE $BOOTNODE
