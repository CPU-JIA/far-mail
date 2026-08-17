# FAR Mail

**Free Account Registration Mail**，面向验证流程的可编程临时邮件服务。项目采用单站长控制台，支持多域名、自动建箱、验证码与验证链接提取、专用 API Token，以及 Go API + Postfix 收件链路。

---

## 功能特性

| 功能 | 说明 |
|------|------|
| 自动建箱 | 任意地址首封来信自动建箱，支持根域与子域 |
| 查最新验证码 | `lookup/latest-code` 直接返回预计算验证码 |
| 查最新链接 | `lookup/latest-link` 直接返回预计算验证链接 |
| 多域名池 | 多个域名用于创建邮箱，并保留公开域名捐献入口 |
| MX 自动验证 | 提交域名后后台自动检测 MX，通过后自动激活 |
| 捐献奖励 | 验证域名后激活限额 API Token，支持续捐累加、周期复检、失效回收和后台调额 |
| 站长控制台 | Vite + Vue 3 + TypeScript 单管理员后台，提供仪表盘、邮箱目录、域名、Token、数据统计、运维和站点设置 |
| 访问令牌 | 站长可签发多个专用 API Token，支持轮换、吊销、速率与总量限制 |
| 收件主链 | Postfix 负责 SMTP 前端，Go API 负责投递、查询与维护 |
| 热路径投影 | `mailbox_state` 预计算最新验证码与验证链接 |

---

## 快速启动

### 前置条件

- Docker 20.10+
- Docker Compose v2+
- 公网 IP / 域名（用于接收邮件）

### 1. 克隆并配置

```bash
git clone <repo-url>
cd far-mail
cp .env.example .env
# 编辑 .env，填写 SMTP_SERVER_IP 和 SMTP_HOSTNAME
```

### 2. 启动服务

```bash
docker compose up -d
```

默认会启动这些容器：

- `postgres`
- `pgbouncer`
- `redis`
- `api`
- `postfix`
- `frontend`（独立静态服务，只提供 Vite 构建产物与 SPA fallback）

### 3. 获取后台管理密钥

首次启动后，Admin Key 会写入 `data/admin.key`：

```bash
cat data/admin.key
# ADMIN_AUTH_KEY=sk-mail-<32位十六进制>
```

### 4. 访问 Web 界面

本地浏览器打开 `http://127.0.0.1:8889`，Go API 健康检查位于 `http://127.0.0.1:18081/health`。生产环境由部署者选择 Nginx、Caddy、Traefik 或其他 TLS 反代，固定路由契约与可直接使用的示例见 [`docs/reverse-proxy.md`](docs/reverse-proxy.md)。登录密钥格式是 `sk-<自定义>-<16或32位hex>`，默认 `sk-mail-<32位hex>`；也可在站点设置中直接更新。

---

## 环境变量

在项目根目录 `.env` 文件中配置（**所有含服务器 IP / 域名的信息均在此处填写，不写入代码**）：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SMTP_SERVER_IP` | *(必填)* | 服务器公网 IP，用于 MX 验证与 SPF 生成 |
| `SMTP_HOSTNAME` | *(推荐填写)* | 邮件服务器主机名，如 `mail.yourdomain.com`。设置后用户添加域名只需一条 MX 记录，无需 A 记录 |
| `API_DB_DSN` | `postgres://...@pgbouncer:6432/far_mail?sslmode=disable` | Go API 数据库连接串 |
| `API_REDIS_ADDR` | `redis:6379` | Redis 地址 |
| `API_REDIS_PASSWORD` | *(按 .env 配置)* | Redis 密码 |
| `API_PORT` | `8080` | Go API 容器内监听端口 |
| `GOPROXY` | `https://goproxy.cn,direct` | Go Docker 构建依赖镜像，可按部署网络覆盖 |
| `NPM_REGISTRY` | `https://registry.npmmirror.com` | 前端 Docker 构建依赖镜像，可按部署网络覆盖 |
| `ADMIN_KEY_FILE` | `/data/admin.key` | Admin Key 写入路径（容器内）|
| `INTERNAL_SYNC_KEY` | *(必填)* | API 与 Postfix 的私有域名同步密钥，不得使用模板默认值 |
| `CONSOLE_ORIGIN` | `http://localhost:8889` | 允许访问站长控制台 API 的精确浏览器 Origin |
| `PUBLIC_API_ORIGIN` | `http://127.0.0.1:18081` | 浏览器访问的 API Origin；生产同源反代时留空 |
| `EDGE_NETWORK_NAME` | `far-mail-edge` | 仅供 frontend、API 与可选外部反代互联的 Docker network |
| `TRUSTED_PROXY_CIDRS` | `172.16.0.0/12` | 允许读取 `X-Forwarded-For` 的反向代理 CIDR，按实际边缘代理调整 |
| `DISABLE_CLEANUP_LOOP` | `false` | 仅在维护窗口临时停止过期邮箱/邮件清理循环 |
| `DISABLE_DOMAIN_HEALTH_LOOP` | `false` | 仅在维护窗口临时停止域名健康刷新循环 |
| `DISABLE_MX_VERIFIER_LOOP` | `false` | 仅在维护窗口临时停止捐赠与后台域名 MX 验证循环 |
| `AUTO_CLEAN_INTERVAL_SECONDS` | `60` | 清理循环间隔；清理范围由后续 `AUTO_CLEAN_*` 设置控制 |
| `AUTO_CLEAN_EMAIL_MAX_AGE_MINUTES` | `1440` | 自动清理邮件的最大保留分钟数 |
| `AUTO_CLEAN_EMAIL_MAX_COUNT` | `30000` | 自动清理前保留的邮件总量上限 |

`.env` 示例：

```dotenv
SMTP_SERVER_IP=192.0.2.10
SMTP_HOSTNAME=mail.yourdomain.com
INTERNAL_SYNC_KEY=replace_with_generated_64_hex
```

> `SMTP_SERVER_IP` / `SMTP_HOSTNAME` 也可在管理后台「系统设置」中修改，DB 值优先于环境变量。

后台循环开关只用于临时维护或故障隔离，正常部署应保持三个 `DISABLE_*` 值为 `false`。完整的收件队列、正文大小、worker 数量和 Postfix 调度参数见 `.env.example`，这些参数应根据机器资源与实际邮件流量调整。

---

## 添加邮件域名

### 方式一：公开捐献域名

1. 打开 `/donate`，提交一个自己控制的根域名
2. 选择创建新的奖励 API Token，或用已有奖励 API Token 继续累加域名
3. 按页面展示配置收件 MX 和唯一 TXT challenge
4. MX 与 TXT 同时验证通过后，域名加入收件池并激活对应奖励
5. 新奖励 API Token 只在创建响应中显示一次；状态查询使用当前标签页保存的独立 claim secret

默认每个验证通过并加入域名池的域名，为所属奖励 Token 增加 `30 RPM / 5,000 daily / 100,000 total`。站长可在「捐赠计划」查看奖励密钥、域名记录与额度流水，修改规则、应用到现有奖励池、立即复检、撤销或恢复奖励，并对奖励 API Token 人工增减额度。

### 方式二：站长后台添加

登录站长后台 → 域名管理 → 添加域名。系统会立即检测 MX，未生效时进入自动验证队列。

捐献奖励的数据模型、状态机与接口见 [`docs/domain-donation-rewards.md`](docs/domain-donation-rewards.md)。

### 所需 DNS 记录

**已配置 `SMTP_HOSTNAME`（推荐）**——仅需 2 条记录：

```text
MX   @   mail.yourdomain.com   优先级 10
TXT  @   v=spf1 ip4:<服务器IP> ~all
```

公开捐献还必须添加页面给出的唯一 TXT 记录：

```text
TXT  _far-mail-donate  far-mail-site-verification=<challenge>
```

> `mail.yourdomain.com` 为 `SMTP_HOSTNAME` 的值，A 记录由该主机名自身提供，用户域名无需额外 A 记录。

**未配置 `SMTP_HOSTNAME`**——需 3 条记录：

```text
MX   @              mail.example.com   优先级 10
A    mail           <服务器公网 IP>
TXT  @              v=spf1 ip4:<服务器公网 IP> ~all
```

---

## 接口与凭据边界

| 通道 | 路径 | 凭据 | 用途 |
|------|------|------|------|
| 站长控制台 | `/console/v1/*` | `X-Admin-Key: sk-<自定义>-...` | Web 后台管理，不对脚本开放 |
| 自动化 API | `/api/v1/*` | `Authorization: Bearer <32位hex>` | 邮箱与邮件自动化调用 |
| 公开接口 | `/public/v1/*` | 提交无需凭据；状态查询在 JSON body 携带 claim secret | 公开配置、首次捐献和状态查询 |
| 奖励续捐 | `POST /api/v1/donations` | `Authorization: Bearer <32位hex>` | 将新域名累加到已有奖励 API Token，不消耗调用额度 |

后台密钥只查询 `accounts.api_key`，API Token 只查询 `account_tokens`。Admin Key 带 `sk-` 前缀，API Token 是纯 32 位小写 hex；两个通道没有回退逻辑，不能交叉登录或调用。

旧的无版本接口不会回退到 SPA：`/api/*`、`/v1/*`、`/auth/*`、`/register*` 和 `/accounts*` 均明确返回 404。

自动化 API 使用 Bearer API Token：

```http
Authorization: Bearer <access_token>
```

### 标准调用示例

```bash
BASE="https://mail.example.com"
API="$BASE/api/v1"
PUB="$BASE/public/v1"
TOKEN="your_access_token"

# 公开配置
curl -s "$PUB/settings"

# 创建邮箱
curl -s -X POST "$API/mailboxes" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"address":"testbox","domain":"example.com"}'

# 按地址直接查验证码（推荐）
curl -s "$API/lookup/latest-code?address=testbox@example.com" \
  -H "Authorization: Bearer $TOKEN"

# 按地址直接查验证链接（推荐）
curl -s "$API/lookup/latest-link?address=testbox@example.com" \
  -H "Authorization: Bearer $TOKEN"

# 查最新邮件摘要
curl -s "$API/lookup/latest?address=testbox@example.com" \
  -H "Authorization: Bearer $TOKEN"
```

### 统一错误模型

错误响应保持兼容 `error` 字段，同时补充：

```json
{
  "success": false,
  "error": "mailbox not found",
  "message": "mailbox not found",
  "error_code": "not_found",
  "status": 404,
  "request_id": "7d5f5a2c-..."
}
```

### 常用响应头

```text
X-Request-ID
X-RateLimit-Limit
X-RateLimit-Remaining
X-RateLimit-Reset
X-Token-Scope
X-Token-Daily-Remaining
X-Token-Total-Remaining
```

Token 的分钟/每日限制使用 Redis 固定窗口原子计数；Redis 无法执行限流时返回 `503 redis_unavailable`，不会静默放行。每日统计和额度按 Asia/Shanghai 日期计算。

完整的机器可读接口契约见 [`docs/openapi.yaml`](docs/openapi.yaml)。契约使用相对 Server URL，导入 Swagger UI、Redoc 或代码生成器后，应由调用方填写自己的实际部署来源；不会携带示例站点的固定地址。后台「开发文档」页面会从当前浏览器来源生成 Base URL，并可一键复制完整 Markdown。

---

## 运维与性能

后台「运维中心」读取真实部署状态，包括 PostgreSQL/Redis Ping、SMTP 有效配置与来源、25 端口可达性、LMTP 监听/队列/worker/失败数、域名 MX 健康和 `/api/v1` 调用趋势。仪表盘的邮箱和邮件总数来自数据库总计，不依赖当前列表分页。

- 页面按路由懒加载，主 JS 当前约 56 KB gzip（约 145 KB 原始体积，具体以构建输出为准）。
- 收件箱与捐献域名轮询在标签页不可见时暂停，并防止请求重叠。
- System Summary 缓存 2 秒，SMTP TCP 探测缓存 10 秒。
- API 调用观测使用有界内存队列批量写入，保留 14 天；不记录凭据、query、body 或响应内容。
- 清理操作执行前可预估过期邮箱、空邮箱、历史邮件数量与大致空间；诊断快照复制前会自动脱敏。
- Go/PgBouncer 与 Redis 使用小型有界连接池（Go DB pool 上限 32、Redis 最多保留 4 条 idle），降低单站长小服务器的空闲连接和内存占用。
- 邮箱分页的总数与数据在同一条 SQL/连接中返回；Redis pool 最大保留 4 条 idle 连接，避免短时并发后的空载连接膨胀。
- frontend 与 API 运行在独立容器，前端运行时 API Origin 可切换同源或独立地址，无需重新构建 Vite 资源。
- Go Dockerfile 先缓存 modules，使用 `GOPROXY=https://goproxy.cn,direct`。

完整冒烟检查、鉴权边界和故障排查见 [`docs/operator-runbook.md`](docs/operator-runbook.md)，组件与数据流见 [`docs/architecture.md`](docs/architecture.md)。
上线前凭据隔离、捐赠反滥用、Cloudflare 回滚和负向测试清单见 [`docs/security-review.md`](docs/security-review.md)。

---

## 数据库迁移

| 文件 | 用途 |
|------|------|
| `sql/init.sql` | 全量初始化（新库使用）|
| `sql/migrate_v2.sql` | v1 → v2：添加邮箱 `expires_at` 字段 |
| `sql/migrate_v3.sql` | v2 → v3：域名 `status`、`mx_checked_at`，新增系统配置项（含 `smtp_hostname`）|
| `sql/migrate_v4.sql` | v3 → v4：邮箱新增 `keep_forever`，支持永久保留 |
| `sql/migrate_v5.sql` | v4 → v5：允许 `mailbox_ttl_minutes=0` 表示永久保留 |
| `sql/migrate_v6.sql` | v5 → v6：新增 `mailbox_state` 最新邮件投影 |
| `sql/migrate_v7.sql` | v6 → v7：补齐投影域名字段与筛选索引 |
| `sql/migrate_v8.sql` | v7 → v8：补齐验证码/链接解析字段 |
| `sql/migrate_v9.sql` | v8 → v9：优化投影表写入 fillfactor |
| `sql/migrate_v10.sql` | v9 → v10：移除未使用的投影 recency 索引 |
| `sql/migrate_v11.sql` | 后台管理密钥与 API Token 分离 |
| `sql/migrate_v12.sql` | 清理旧注册设置，后台密钥升级为可配置的 `sk-自定义-16/32hex` 格式 |
| `sql/migrate_v13.sql` | 将旧默认站点名称迁移为 `FAR Mail`，不覆盖站长自定义名称 |
| `sql/migrate_v14.sql` | 邮箱列表与邮件时间热路径索引；并移除重复的单列账号索引 |
| `sql/migrate_v15.sql` | 捐赠奖励人工调整流水的窄 partial index |
| `sql/migrate_v16.sql` | 重建 `mailbox_state` 最新邮件、验证码/链接投影与计数 |
| `sql/migrate_v17.sql` | 为邮箱域名筛选添加账号范围/时间排序表达式索引 |
| `sql/migrate_v18.sql` | 增加脱敏的 Cloudflare/集成操作审计表 |

现有部署升级到当前凭据边界、品牌默认值、性能索引和集成审计时，按当前数据库版本依次执行缺失的迁移（至少 v12 到 v18）。下面命令假设 `.env` 使用默认的 `far_mail` 数据库和用户：

```bash
docker exec -i $(docker compose ps -q postgres) \
  psql -U far_mail -d far_mail < sql/migrate_v12.sql
docker exec -i $(docker compose ps -q postgres) \
  psql -U far_mail -d far_mail < sql/migrate_v13.sql
docker exec -i $(docker compose ps -q postgres) \
  psql -U far_mail -d far_mail < sql/migrate_v14.sql
docker exec -i $(docker compose ps -q postgres) \
  psql -U far_mail -d far_mail < sql/migrate_v15.sql
docker exec -i $(docker compose ps -q postgres) \
  psql -U far_mail -d far_mail < sql/migrate_v16.sql
docker exec -i $(docker compose ps -q postgres) \
  psql -U far_mail -d far_mail < sql/migrate_v17.sql
docker exec -i $(docker compose ps -q postgres) \
  psql -U far_mail -d far_mail < sql/migrate_v18.sql
```

v14 与 v17 使用 `CREATE/DROP INDEX CONCURRENTLY`，必须在事务外执行；它不会阻塞
LMTP 收件与邮箱创建。`idx_mailboxes_account_created` 支持账号邮箱按创建时间倒序
分页，`idx_mailboxes_account_domain_created` 支持精确域名筛选，`idx_emails_received_at` 支持历史清理、总量裁剪、活动统计和短期统计。没有
为高频更新的 `mailbox_state` 重新添加按账号/时间索引，以避免每封邮件更新时的写放大。

当前 API 启动时还会幂等创建辅助 schema，包括 `account_tokens`、`domain_donations`、`donation_reward_events`、`api_request_events`、`mailbox_state`，并补齐 `domains.source_type` 与 `mailboxes.creator_token_id`。重建 API 前先备份 PostgreSQL；启动后检查 API 日志与上述表/字段。

生产备份、SHA-256 校验、临时数据库恢复演练与 Redis 持久化策略见 [`docs/operator-runbook.md`](docs/operator-runbook.md#backup-and-restore)。Windows 可直接运行 `scripts/backup.ps1` / `scripts/restore.ps1`，Linux 可使用 `sh scripts/backup.sh` / `sh scripts/restore.sh`。

---

## 项目结构

```
far-mail/
├── frontend/             # Vite + Vue 3 + TypeScript 站长控制台
│   ├── src/              # 页面、组件、状态与类型化 API 客户端
│   ├── css/style.css     # 现有视觉样式
│   ├── server/           # Go 静态服务器，不代理 API
│   ├── Dockerfile
│   └── package.json
├── api/                  # Go API 服务
├── docs/                 # 运维与接手文档
├── pgbouncer/            # PgBouncer 连接池配置
├── postfix/              # Postfix 收件前端
├── sql/                  # 数据库 DDL 与迁移脚本
├── data/                 # 运行时数据（admin.key 在此，已 gitignore）
├── docker-compose.yml
├── AGENTS.md             # 项目长期约束与验证命令
└── .env                  # 敏感配置（已 gitignore，不含硬编码 IP）
```

---

## 后台服务职责

| 服务 | 说明 |
|------|------|
| Postfix | SMTP 前端，负责接收公网邮件并通过 LMTP 投递给 Go API |
| api | HTTP API、Go LMTP 收件入口、控制台、访问令牌、域名配置、自动建箱与投影更新 |
| frontend | 独立静态服务，提供 Vite 构建产物、runtime config 与 SPA fallback，不代理 API |
| PgBouncer | 稳定数据库连接数，承接 Go API 高并发请求 |

---

## 许可证

MIT
