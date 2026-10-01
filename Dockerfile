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
RUN go build -ldflags="-s -w -X keyhive/internal/version.Version=${VERSION}" -o /out/keyhive ./cmd/keyhive

# ---- 运行镜像 ----
FROM alpine:3.21
RUN adduser -D -H -u 1000 keyhive && apk add --no-cache ca-certificates tzdata
USER keyhive
WORKDIR /app
COPY --from=build /out/keyhive /app/keyhive
ENV KEYHIVE_DATA=/app/data KEYHIVE_ADDR=:8020
VOLUME ["/app/data"]
EXPOSE 8020
ENTRYPOINT ["/app/keyhive"]
