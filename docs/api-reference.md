# FAR Mail API 调用参考

这份文档对应当前 Go API。部署地址使用 `BASE_URL` 占位符，调用时替换成自己的真实来源；项目不会把某个服务器 IP 或域名写死。

机器可读契约：[openapi.yaml](openapi.yaml)。后台「开发文档」页面会根据当前浏览器来源生成相同内容，并提供一键复制 Markdown。

## 凭据边界

| 通道 | 认证 | 范围 |
| --- | --- | --- |
| 自动化 API | `Authorization: Bearer <API_TOKEN>` | `/api/v1/*` |
| 首次捐赠 | 无 | `POST /public/v1/domains/submit` |
| 捐赠状态 | JSON body 中的 `claim_secret` | `POST /public/v1/domains/status` |
| 站长控制台 | `X-Admin-Key: <ADMIN_KEY>` | `/console/v1/*`，仅后台内部使用 |

Admin Key 和 API Token 来自不同存储、不同签发渠道，不能互相替代。Admin Key 使用 `sk-<custom>-<16|32 hex>`，API Token 使用纯 32 位小写 hex。不要使用 `X-API-Key`、query-string key 或旧版无版本接口。

## 最小调用流程

```bash
BASE_URL="https://<your-deployment-host>"
API="$BASE_URL/api/v1"
PUBLIC="$BASE_URL/public/v1"
TOKEN="<api-token>"

# 读取真实部署公开配置（包括 SMTP/DNS 指引来源）
curl -fsS "$PUBLIC/settings"

# 读取活动根域池
curl -fsS "$API/domains" \
  -H "Authorization: Bearer $TOKEN"

# 创建邮箱；domain 可传根域或由调用方生成的随机子域
curl -fsS -X POST "$API/mailboxes" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"address":"verify","domain":"<active-root-domain>"}'

# 按地址读取最新验证码
curl -fsS "$API/lookup/latest-code?address=verify%40<active-root-domain>" \
  -H "Authorization: Bearer $TOKEN"
```

## 邮件读取方式

- `GET /api/v1/mailboxes/:id/emails`：分页读取邮件摘要。
- `GET /api/v1/mailboxes/:id/emails/:email_id`：读取正文、Headers、验证码和验证链接。
- `GET /api/v1/mailboxes/:id/events`：SSE 实时事件；服务端发送 `ready`、`email` 和 `heartbeat`。
- `GET /api/v1/lookup/latest`、`latest-code`、`latest-link`：按完整邮箱地址读取最新投影，适合短轮询或脚本调用。

## 域名捐赠

首次捐赠不需要 API Token：

```bash
curl -fsS -X POST "$PUBLIC/domains/submit" \
  -H "Content-Type: application/json" \
  -d '{"domain":"<your-root-domain>","enable_subdomains":true}'
```

响应中的完整 `access_token` 与 `claim_secret` 只返回一次。MX 和每次捐赠独立的 TXT challenge 都验证通过后，奖励额度才会生效。使用奖励 Token 续捐时：

```bash
curl -fsS -X POST "$API/donations" \
  -H "Authorization: Bearer <donation-token>" \
  -H "Content-Type: application/json" \
  -d '{"domain":"<another-root-domain>","enable_subdomains":true}'
```

`POST /public/v1/domains/status` 使用 `donation_id` 与 `claim_secret` 查询状态；不存在可枚举的 GET 状态接口。DNS 记录必须以响应中的 `dns_required` 为准，因为 SMTP hostname/IP 和 TXT challenge 来自当前部署配置。

## 错误与限流

错误响应包含 `error`、`message`、`error_code`、`status` 和 `request_id`。成功或失败的 API 请求可能包含：

```text
X-Request-ID
RateLimit-Limit
RateLimit-Remaining
RateLimit-Reset
X-Token-Scope
X-Token-Daily-Remaining
X-Token-Total-Remaining
```

Redis 无法执行 Token 限流时返回 `503 redis_unavailable`，不会静默放行。API 观测只保留 Token ID、路由模板、方法、状态、耗时、Request ID 和时间戳，不记录凭据、query、请求体、响应体或邮件内容。

## 当前接口索引

详细参数、状态码和 JSON Schema 以 [OpenAPI 契约](openapi.yaml) 为准：

| 分组 | 接口 |
| --- | --- |
| Domains | `GET /api/v1/domains` |
| Mailboxes | `GET/POST /api/v1/mailboxes`、`GET/DELETE /api/v1/mailboxes/:id`、`PUT /api/v1/mailboxes/:id/retention` |
| Email | `GET /api/v1/mailboxes/:id/emails`、`GET/DELETE /api/v1/mailboxes/:id/emails/:email_id`、`GET /api/v1/mailboxes/:id/events` |
| Lookup | `GET /api/v1/lookup/mailbox`、`latest`、`latest-code`、`latest-link` |
| Cleanup | `POST /api/v1/mailboxes/cleanup`、`POST /api/v1/emails/cleanup` |
| Donation | `POST /public/v1/domains/submit`、`POST /public/v1/domains/status`、`POST /api/v1/donations` |
| Public | `GET /public/v1/settings`、`GET /public/v1/logo` |
| System | `GET /health` |
