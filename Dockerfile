# keyHive 构建镜像：默认国外源，USE_CN_MIRROR=1 切换国内源（仅公网镜像源，无内网地址）

# ---- 前端构建 ----
FROM node:24-alpine AS web
ARG USE_CN_MIRROR=0
WORKDIR /src/web
COPY web/package*.json ./
RUN if [ "$USE_CN_MIRROR" = "1" ]; then npm config set registry https://registry.npmmirror.com; fi \
    && npm ci
COPY web/ ./
RUN npm run build

# ---- 后端构建 ----
FROM golang:1.26-alpine AS build
ARG USE_CN_MIRROR=0
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN if [ "$USE_CN_MIRROR" = "1" ]; then go env -w GOPROXY=https://goproxy.cn,direct; fi
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN go build -ldflags="-s -w -X github.com/lpx0312/keyHive/internal/version.Version=${VERSION}" -o /out/keyhive ./cmd/keyhive

# ---- 运行镜像 ----
FROM alpine:3.21
# 构建期预创建数据目录并授权（对齐 adduser -u 1000）：否则非 root 首次以匿名卷/命名卷启动时写库 EACCES
RUN adduser -D -H -u 1000 keyhive && apk add --no-cache ca-certificates tzdata \
    && mkdir -p /app/data && chown 1000:1000 /app/data
USER keyhive
WORKDIR /app
COPY --from=build /out/keyhive /app/keyhive
ENV KEYHIVE_DATA=/app/data KEYHIVE_ADDR=:8020
VOLUME ["/app/data"]
EXPOSE 8020
ENTRYPOINT ["/app/keyhive"]
