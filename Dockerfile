# =========================
# 1. Build stage
# =========================
FROM golang:1.25-alpine AS builder

# 开启 Go modules，并开启更严格的构建
ENV GO111MODULE=on \
    CGO_ENABLED=0

# 安装构建依赖（如果需要私有仓库证书等，可以在这里扩展）
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories
RUN apk add --no-cache ca-certificates tzdata

# 设置工作目录
WORKDIR /app

RUN go env -w GOPROXY='https://goproxy.io,direct'

# 先只拷贝 go.mod / go.sum，加速依赖缓存
COPY go.mod go.sum ./
RUN go mod download

# 再拷贝剩余源码
COPY . .

# 编译二进制（根据你的项目入口调整路径）
# 比如 main 在 ./cmd/server/main.go
RUN go build -o server ./cmd/server

# =========================
# 2. Runtime stage
# =========================
FROM alpine:3.22 AS runtime

# 安装运行时依赖
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories
RUN apk add --no-cache ca-certificates tzdata

# 设置时区（可选）
ENV TZ=Asia/Shanghai

# 创建非 root 用户（更安全）
RUN addgroup -S app && adduser -S app -G app

WORKDIR /app

# 从 builder 拷贝编译好的二进制
COPY --from=builder /app/server /app/server

# 如果有配置文件、静态资源等，也在这里拷贝
COPY ./config/config.yaml /app/config.yaml

# 切换为非 root 用户
USER app

# 暴露端口（按你的服务端口修改）
EXPOSE 8080 8081

# 启动命令
ENTRYPOINT ["/app/server", "-c", "/app/config.yaml"]
