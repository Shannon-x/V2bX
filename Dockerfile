# Build go
# 1.26.4 covers all 17 reachable Go-stdlib advisories that govulncheck flags
# at v1.26.0: net/textproto (GO-2026-5039), crypto/x509 (-5037/-4947/-4946/
# -4866/-4600/-4599), crypto/tls (-4870), html/template (-4982/-4980/-4865/
# -4603), net/http (-4918), net/http/httputil (-4976), net (-4971), os
# (-4602), net/url (-4601). Keep this in sync with .github/workflows/release.yml.
#
# 编译阶段固定跑在构建机的原生架构上（$BUILDPLATFORM），由 Go 交叉编译出
# 目标架构（$TARGETOS/$TARGETARCH）。CGO 关闭，交叉编译没有额外代价；
# 在 x86 上构建 arm64 镜像时也不会再把 go build 丢进 QEMU 里模拟执行。
FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
ENV CGO_ENABLED=0
RUN GOEXPERIMENT=jsonv2 go mod download
COPY . .
RUN GOEXPERIMENT=jsonv2 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -v -o V2bX -tags "sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor" -trimpath -ldflags "-s -w"

# Release
FROM alpine:3.21
RUN apk --update --no-cache add tzdata ca-certificates \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
RUN mkdir -p /etc/V2bX/
COPY --from=builder /app/V2bX /usr/local/bin

ENTRYPOINT [ "V2bX", "server", "--config", "/etc/V2bX/config.json"]
