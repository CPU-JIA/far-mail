# 域名捐赠奖励

域名捐赠是公开入口，不是租户注册。捐赠者提交自己控制的收件域名，完成 MX 与 TXT 验证后获得有额度的奖励 API Token；同一 Token 可以继续捐赠并累加额度。

## 凭据边界

| 凭据 | 传递方式 | 可访问范围 | 存储 |
|---|---|---|---|
| Admin Key | `X-Admin-Key` | `/console/v1/*` | `accounts.api_key` |
| 标准 API Token | `Authorization: Bearer` | `/api/v1/*` | `account_tokens`，`token_kind=standard` |
| 奖励 API Token | `Authorization: Bearer` | `/api/v1/*` 和续捐接口 | `account_tokens`，`token_kind=donation` |
| claim secret | JSON body | `POST /public/v1/domains/status` | `domain_donations.claim_secret_hash` |

Admin Key 与两类 API Token 不共享校验、轮换或 fallback。claim secret 只查询单次捐赠状态，不能调用 API。完整 API Token 和 claim secret 只在创建响应中返回一次，数据库只保存 hash 或关联信息。

## 提交流程

### 创建奖励 API Token

`POST /public/v1/domains/submit`

```json
{
  "domain": "example.com",
  "enable_subdomains": false
}
```

接口返回 `202 Accepted`，响应包括：

- `donation_id`：捐赠记录 UUID。
- `claim_secret`：状态查询凭据，仅返回一次。
- `access_token`：新的 32 位小写 hex 奖励 API Token，仅返回一次。
- `dns_required`：按当前部署配置生成的 MX 与 TXT 记录。

Token 在域名验证完成前额度为零，不能提前消费奖励。

### 使用已有 Token 续捐

`POST /api/v1/donations`

```http
Authorization: Bearer <32位小写hex>
Content-Type: application/json
```

请求 body 与首次提交相同。此接口只接受 `token_kind=donation`，不会消耗该 Token 的调用额度，也不会重新返回完整 Token。默认每个 Token 最多关联 10 个未撤销域名。

### 查询状态

`POST /public/v1/domains/status`

```json
{
  "donation_id": "00000000-0000-0000-0000-000000000000",
  "claim_secret": "<one-time-secret>"
}
```

查询会在上次检查超过 10 秒时尝试一次即时验证。不存在可枚举的 GET 状态接口。

## DNS 与状态

验证同时要求：

```text
MX   @                    <当前 SMTP hostname 或 server IP>
TXT  _far-mail-donate     far-mail-site-verification=<challenge>
```

`dns_required` 始终根据 `.env` 或站长设置中的有效 SMTP 配置生成，不包含写死的生产地址。

```text
pending -> active -> inactive -> active
              |          |
              |          +-- 验证恢复后重新激活原额度
              +------------- 管理员撤销 -> revoked
```

- `pending`：等待 MX 与 TXT 同时通过，不产生额度。
- `active`：域名进入收件池，该域名额度计入奖励 Token。
- `inactive`：验证失效，该域名额度从 Token 中移除。
- `revoked`：管理员撤销，只有后台立即复检操作可以解除撤销并重新验证。
- 明确的 DNS 不匹配立即失活；临时解析错误累计到容忍次数后才失活，默认 3 次。
- 验证成功会恢复同一笔额度，不重复发奖。

后台 verifier 每 30 秒处理 pending 记录；active/inactive 记录按 `donation_recheck_minutes` 复检，默认 30 分钟。

## 额度计算

每个有效域名默认贡献：

```text
30 RPM
5,000 daily
100,000 total
```

Token 的有效额度为所有 `reward_active=true` 域名额度之和，再叠加 `manual_adjust` 台账；RPM 最终受 `donation_token_rate_limit_cap` 限制。默认 RPM 上限为 180。

站长在「捐赠计划」可以：

- 调整新捐赠的 RPM、daily、total 和每 Token 域名上限。
- 配置 DNS 失败容忍次数和复检周期。
- 立即复检、撤销或恢复某条捐赠。
- 对 Token 做人工额度增减。
- 将最新奖励规则应用到已有未撤销捐赠。

应用规则会更新每条域名的基础额度，但人工调整单独保存在事件台账中，不会被覆盖。

## 后台接口

以下接口均要求 `X-Admin-Key`：

| Method | Path | 用途 |
|---|---|---|
| `GET` | `/console/v1/donations` | 域名记录、奖励 Token 聚合、额度流水和汇总 |
| `POST` | `/console/v1/donations/tokens/:id/adjust` | 直接调整指定奖励 Token 的额度 |
| `POST` | `/console/v1/donations/:id/recheck` | 立即复检；撤销记录会先恢复为可复检状态 |
| `POST` | `/console/v1/donations/:id/revoke` | 撤销域名奖励并停用域名 |
| `POST` | `/console/v1/donations/:id/adjust` | 调整所属奖励 Token 的额度 |
| `POST` | `/console/v1/donations/policy/apply` | 将当前规则应用到已有捐赠 |
| `PUT` | `/console/v1/settings` | 保存开关与奖励规则 |

公开页面由 `donation_enabled` 控制。关闭后 `/donate` 不再开放，两个提交接口返回 `403`，已有状态查询和后台管理仍可使用。

## 数据模型

- `domain_donations`：域名、Token、challenge、claim hash、状态、每域额度和验证历史。
- `donation_reward_events`：`grant`、`revoke`、`manual_adjust`、`policy_update` 等额度事件。
- `account_tokens.token_kind`：区分 `standard` 与 `donation`。
- `mailboxes.creator_token_id`：隔离奖励 Token 创建的邮箱；站长控制台仍可查看全站数据。
- `domains.source_type=donated`：区分公开捐赠域名与站长直接管理域名。

所有状态和额度重算在数据库事务中完成，并对共享 Token 加行锁，避免多个域名同时复检时重复计算。

`GET /console/v1/donations` 的 `tokens` 只返回不可用于鉴权的 Token 前缀及聚合额度，`events` 返回最新 200 条奖励事件。完整奖励 Token 仍只在首次签发时返回一次，数据库和后台列表都不保存可恢复明文。

## 默认设置

| Setting | Default |
|---|---:|
| `donation_enabled` | `true` |
| `donation_reward_rate_limit_per_minute` | `30` |
| `donation_reward_daily_request_limit` | `5000` |
| `donation_reward_total_request_limit` | `100000` |
| `donation_token_rate_limit_cap` | `180` |
| `donation_max_domains_per_token` | `10` |
| `donation_dns_failure_tolerance` | `3` |
| `donation_recheck_minutes` | `30` |
