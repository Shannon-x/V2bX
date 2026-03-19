# Build go
FROM golang:1.26.0-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
ENV CGO_ENABLED=0
RUN GOEXPERIMENT=jsonv2 go mod download
COPY . .
RUN GOEXPERIMENT=jsonv2 go build -v -o V2bX -tags "sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor" -trimpath -ldflags "-s -w"

# Release
FROM alpine:3.21
RUN apk --update --no-cache add tzdata ca-certificates \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
RUN mkdir -p /etc/V2bX/
COPY --from=builder /app/V2bX /usr/local/bin

ENTRYPOINT [ "V2bX", "server", "--config", "/etc/V2bX/config.json"]
