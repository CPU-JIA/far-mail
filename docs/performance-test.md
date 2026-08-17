# FAR Mail 性能测试

只读 HTTP 基准工具位于 [`scripts/performance.go`](../scripts/performance.go)。它不创建邮箱、不发送邮件，也不会修改捐赠或 Token 数据；认证值只从进程环境变量读取，不会打印。

## 本地运行

默认测试本地 API 的健康检查和公开设置：

```powershell
go run .\scripts\performance.go
```

常用参数：

```powershell
go run .\scripts\performance.go `
  -base-url http://127.0.0.1:18081 `
  -requests 1000 `
  -concurrency 50 `
  -warmup 10 `
  -output .\performance-result.txt
```

如果要覆盖同源反代后的鉴权接口，在当前 PowerShell 会话临时设置凭据：

```powershell
$env:FAR_MAIL_API_TOKEN = '<32位 API Token>'
$env:FAR_MAIL_ADMIN_KEY = '<Admin Key>'
go run .\scripts\performance.go -base-url https://<your-host> -requests 1000 -concurrency 50
Remove-Item Env:FAR_MAIL_API_TOKEN, Env:FAR_MAIL_ADMIN_KEY -ErrorAction SilentlyContinue
```

工具会输出每个场景的成功数、错误数、平均延迟、P50/P95/P99、总耗时和 RPS。`FAR_MAIL_API_TOKEN` 只启用 `GET /api/v1/domains`，`FAR_MAIL_ADMIN_KEY` 只启用 `GET /console/v1/system/summary`。

## 2026-08-17 SG 基线

测试在 SG 主机本机发起，经过实际 HTTPS Nginx 边缘和 FAR Mail 容器；每个场景 200 请求、并发 20，未包含写入操作。

| 场景 | 成功 | 错误 | 平均 | P50 | P95 | P99 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `/health` | 200/200 | 0 | 19.5 ms | 18.7 ms | 27.3 ms | 32.2 ms |
| `/public/v1/settings` | 200/200 | 0 | 20.4 ms | 19.3 ms | 30.4 ms | 33.0 ms |
| Bearer `GET /api/v1/domains` | 200/200 | 0 | 23.7 ms | 22.5 ms | 33.7 ms | 39.9 ms |

这是服务器本机基线，不代表跨公网客户端的 RTT；外部用户的实测值还会叠加线路、TLS 握手和 DNS 延迟。

## 邮件链路同口径复测

在同一台 SG 主机上，用 1 KB 邮件、500 封、并发 100 复现旧服务记录。新版 Postfix → Go LMTP → PostgreSQL 提交成功率为 500/500，提交吞吐 **565.2 封/秒**，提交 P95 **201.9 ms**；旧记录的两轮分别为 392.9/525.4 封/秒、P95 299.8/250.3 ms。相对旧记录最佳轮次，新版吞吐约高 7.6%，提交 P95 约低 19.3%。

旧库当时的存量与当前新库不同，因此这是一项实机回归信号而非严格同数据集实验；每次调整索引、连接池或 LMTP worker 后，应在相同数据量下复测。500 封测试邮箱及邮件已在复测后删除。

## 空载资源基线

停止旧服务的 PostgreSQL、Redis 和 PgBouncer（保留容器、卷和备份用于回滚）后，FAR Mail 六个容器的 `docker stats --no-stream` 常驻内存约 **120.6 MiB**：PostgreSQL 79.1 MiB、API 12.1 MiB、Postfix 12.5 MiB、Redis 11.9 MiB、PgBouncer 2.3 MiB、前端 2.8 MiB。SG 主机当时可用内存约 1.06 GiB，1 分钟负载约 0.07。

基线要求：错误数为 0；若修改连接池、轮询间隔或清理策略，应重新运行相同请求量与并发度，并同时记录 API、PostgreSQL、Redis 和 Postfix 的资源变化。
