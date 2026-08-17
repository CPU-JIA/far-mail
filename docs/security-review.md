# FAR Mail 上线前安全审计清单

本文记录当前版本的安全边界和上线前必须复核的证据。它不是租户/用户系统审计：产品只有一个站长控制台，公开捐赠只产生受限的 Reward Token。

## 已确认的控制

| 区域 | 控制 | 验证依据 |
| --- | --- | --- |
| Admin Key | `X-Admin-Key` 只允许 `/console/v1`，格式为 `sk-<custom>-<16|32 hex>` | `api/store/tokens_test.go`、认证中间件测试 |
| API Token | 纯 32 位小写 hex；`Authorization: Bearer` 只允许 `/api/v1`，独立哈希存储和额度计数 | `api/store/tokens.go`、`api/middleware` |
| 凭据隔离 | Admin Key、标准 API Token、捐赠 Reward Token、claim secret 不互相 fallback | `api/middleware/auth_headers_test.go`、路由回归 |
| 旧接口 | `/api/*`、`/v1/*`、`/auth/*`、`/register*`、`/accounts*` 明确返回 404 | Docker 运行态冒烟检查 |
| 公开捐赠 | `/public/v1` 限流；状态查询必须提交独立 claim secret；不存在可枚举 GET 状态 | `api/main.go`、`docs/domain-donation-rewards.md` |
| 奖励额度 | MX + 每笔捐赠独立 TXT challenge 双验证；共享 Token 使用数据库事务和行锁 | `api/store/donations.go`、捐赠并发测试 |
| 奖励访问 | Reward Token 只能读取其创建的邮箱；站长控制台仍可查看全站数据 | `mailboxes.creator_token_id` 及 scoped 查询 |
| DNS 自动化 | Cloudflare 先 Preview，再按安全项/明确确认 Apply；后续写入失败自动回滚 | `api/integrations/cloudflare_test.go` |
| 集成审计 | Cloudflare 预览、应用、凭据更新和回滚只记录脱敏元数据 | `integration_audit_events`、运维中心审计列表 |
| 观测数据 | API telemetry 只保留 Token ID、路由模板、方法、状态、延迟、Request ID、时间 | `api_request_events` schema 与观测中间件 |
| 密钥日志 | 密钥、请求 body、query、邮件正文和供应商响应不进入日志或诊断快照 | `docs/operator-runbook.md` |

## 上线前操作

1. 将 `.env` 中的数据库、Redis、内部同步密钥替换为随机值；不要使用 `.env.example` 的模板值。
2. 确认 `data/admin.key` 和 `data/integrations.json` 仅由 API 进程用户可读，并从备份、日志和工单附件中排除明文密钥。
3. 在站点设置中生成一次新的 Admin Key，并用新 key 验证登录；旧 key 失效后再关闭旧会话。
4. API Token 使用最小 scope 和最小额度；不再使用的 Token 立即暂停或吊销。
5. Cloudflare Token 仅授予目标 Zone 的 `Zone:Read` 与 `DNS:Edit`，优先放入环境变量或受限 secret 文件。
6. 在「运维中心」确认数据库、Redis、SMTP、LMTP 和活动域名状态卡均正常。
7. 使用真实域名执行 DNS Preview；只有确认记录内容、Zone 和冲突后才 Apply。
8. 执行备份并核对相邻 SHA-256 文件；恢复演练必须使用隔离数据库。

## 必须保持的负向测试

- Admin Key 作为 Bearer Token 调用 `/api/v1` 返回 401。
- API Token 作为 `X-Admin-Key` 调用 `/console/v1` 返回 401。
- 通过 `X-API-Key`、query-string key、旧路径调用返回 401/404，不得 fallback。
- 缺少 claim secret、错误 claim secret 或他人 donation ID 不得泄露状态详情。
- 未验证的捐赠域名不产生可消费额度。
- DNS 失效会撤回该域名贡献的额度；恢复后只恢复一次，不重复发奖。
- Reward Token 超过域名上限、RPM、daily 或 total 限额时必须拒绝且不写入邮箱/邮件数据。
- Cloudflare 多条同名记录始终保持冲突状态，不自动删除用户记录。

## 当前不在产品范围内

- 不提供多租户、注册、账户切换或自助用户中心。
- 不支持通过 URL 传递任何凭据。
- 不把 Admin Key 当作 API Token，也不把 Cloudflare Token 返回给浏览器或审计接口。
