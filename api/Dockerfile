# ============================================================
# Go API 多阶段构建 - 最小化镜像
# ============================================================
FROM golang:1.25.12-alpine3.24 AS builder

WORKDIR /build
# Keep module downloads independent from the Docker registry mirror settings.
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/api-server .

# ==================== 运行阶段 ====================
FROM alpine:3.24.1

RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Shanghai

COPY --from=builder /build/api-server /usr/local/bin/api-server

EXPOSE 8080

CMD ["api-server"]
