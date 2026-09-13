# 生产部署

本文给出当前版本可执行的单实例生产部署基线：MarketplaceServer binary + PostgreSQL + 本地持久化 POSIX Git storage + TLS reverse proxy。它不把仓库根目录中的旧 `Dockerfile` 和 Compose 模板视为生产资产；这些文件仍包含通用 jframe 的 MySQL/Redis 与未固定工具链假设。

精确配置键以 [`config.example.yaml`](../config.example.yaml) 为准，备份、恢复和验证要求见 [运维、测试与交付](operations.md)。

## 1. 支持边界

当前推荐部署：

```text
Internet
   │ HTTPS
   ▼
TLS reverse proxy
   │ HTTP :8080 (loopback/private network)
   ▼
MarketplaceServer (single instance)
   ├── PostgreSQL
   └── persistent POSIX filesystem
       └── Git repositories, projections and quarantine
```

- 生产数据库只使用 PostgreSQL。SQLite 仅支持单进程本地开发和测试；MySQL 不受支持。
- 首次生产部署使用一个应用实例。多实例迁移已按 PostgreSQL schema 串行化，但 Git storage 仍要求所有实例看到一致的 POSIX rename/locking 语义；在满足该条件或完成 repository placement 前不要水平扩容。
- Git storage 不能放在容器临时层，也不能直接以普通 object storage 代替 filesystem。
- 当前没有 SSH Git listener；clone、fetch 和 push 使用 HTTPS Smart HTTP。
- `/api/v1/health` 当前只确认 backend 已完成装配，不是数据库和 storage 的持续深度探针。进程成功启动意味着初始数据库连接、migration 和 Git storage 初始化已通过；外部监控仍应增加 PostgreSQL 与 filesystem 检查。
- receive/orphan/projection reconciliation 当前只有代码内可调用能力，没有常驻 worker 或 operator API。发生未完成 intent 或 DB/Git 不一致时，保持写入关闭并使用对应 release 的诊断/修复工具；在交付这类工具前，不能宣称无人值守自动恢复。

## 2. 主机与依赖

部署主机至少需要：

- Linux x86-64 或 arm64；
- PostgreSQL；
- 系统 `git` binary；
- TLS reverse proxy，例如 Nginx、Caddy 或云负载均衡器；
- 构建机上的 Go 版本以 [`go.mod`](../go.mod) 为准；
- 仅在重新构建 frontend 时需要 Node/npm。

生产运行用户必须能执行 `git`，并独占读写 Git storage。建议目录：

```text
/etc/marketplace-server/config.yaml
/etc/marketplace-server/secrets.env
/var/lib/marketplace-server/git/
/var/log/marketplace-server/
/usr/local/bin/marketplace-server
```

## 3. 构建 release binary

在可信构建环境中执行完整静态前端和 Go 构建：

```bash
cd mod/frontend/web
npm ci
npm run check
npm test
npm run build
cd ../../..

git diff --check
go test ./...
go test -race ./...
go vet ./...

VERSION="$(git rev-parse --short HEAD)"
CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags "-s -w -X github.com/Esonhugh/MarketplaceServer/conf.SysVersion=${VERSION}" \
  -o marketplace-server .
```

`mod/frontend/dist` 会嵌入 binary，生产服务器不需要 Node。`npm ci` 使用已提交 lockfile，但 frontend manifest 仍有 `latest` 依赖声明；发布流程不得重新生成 lockfile，并应记录 Node/npm/Go 版本。完全可复现构建仍需后续固定这些依赖声明。将 binary、commit ID、工具链信息和校验和一起发布，例如：

```bash
sha256sum marketplace-server
install -o root -g root -m 0755 marketplace-server /usr/local/bin/marketplace-server
```

若发布目标不是构建机平台，应显式设置 `GOOS=linux` 与匹配的 `GOARCH`，并在目标平台运行真实 Git Smart HTTP 验收。

## 4. PostgreSQL

使用独立 database 和最小范围 application role。以下名称仅为示例：

```sql
CREATE ROLE marketplace_server LOGIN PASSWORD 'replace-with-generated-secret';
CREATE DATABASE marketplace_server OWNER marketplace_server;
```

应用启动时自动执行 migration，因此 application role 当前必须能够在目标 schema 中创建和修改 table、index、constraint、trigger，并获取 PostgreSQL advisory lock。不要把 superuser credential 交给应用。

推荐 DSN 通过受保护配置文件提供；注意 URL 格式密码必须正确 percent-encode。远程 PostgreSQL 应校验服务端证书和主机名，例如：

```yaml
sql:
  driver: "postgres"
  dsn: "postgres://marketplace_server:REDACTED@db.example.internal:5432/marketplace_server?sslmode=verify-full&sslrootcert=/etc/marketplace-server/postgresql-ca.pem"
  debug: false
  maxIdleConns: 5
  maxOpenConns: 20
  connMaxLifetime: "30m"
```

同机 Unix socket 或受控私网的连接参数应按实际部署调整。不要在远程 TCP 基线上用 `sslmode=require` 冒充证书身份校验。应用错误不会输出完整 DSN，但配置文件、process environment、数据库日志和运维系统仍属于 credential boundary。

## 5. 配置与 secrets

创建运行用户和目录：

```bash
useradd --system --home /var/lib/marketplace-server --shell /usr/sbin/nologin marketplace-server
install -d -o marketplace-server -g marketplace-server -m 0700 /var/lib/marketplace-server/git
install -d -o marketplace-server -g marketplace-server -m 0750 /var/log/marketplace-server
install -d -o root -g marketplace-server -m 0750 /etc/marketplace-server
```

复制 [`config.example.yaml`](../config.example.yaml) 为 `/etc/marketplace-server/config.yaml`。该文件是唯一完整 operator 配置清单；生产至少覆盖：

- `mode: "production"`；
- `port: "8080"`；进程当前绑定所有 host interface，必须由 host firewall/security group 只允许 reverse proxy 访问；
- `log.logPath: "/var/log/marketplace-server/server.log"`；
- `sql.driver: "postgres"` 和上一节所述的受保护 DSN，保持 `sql.debug: false`；
- `git.storageRoot: "/var/lib/marketplace-server/git"`；
- `backend.jwtSecret: ""`，改用环境中的 `MARKETPLACE_JWT_SECRET`；
- `backend.registrationEnabled: false`，除非 operator 明确开放公开注册；
- `frontend.basePath: "/"`。

全局 `jin.readTimeout`/`writeTimeout` 同时约束 REST、Git Smart HTTP 和 distribution。默认 30 秒可能截断较慢的 Git 操作，而 Git subprocess 自身还有当前不可配置的 5 分钟 service timeout。按部署网络把 HTTP 超时提高到不短于可接受的 Git 操作窗口，但不要写成超过 5 分钟就一定可用；生产验收必须覆盖预期最大 push/fetch。

保护配置：

```bash
chown root:marketplace-server /etc/marketplace-server/config.yaml
chmod 0640 /etc/marketplace-server/config.yaml
```

生成 secrets。API-key pepper 必须是 Base64，解码后至少 32 bytes；JWT secret 和首次管理员密码无需 Base64：

```bash
printf 'MARKETPLACE_API_KEY_PEPPER=%s\n' "$(openssl rand -base64 48 | tr -d '\n')"
printf 'MARKETPLACE_JWT_SECRET=%s\n' "$(openssl rand -base64 48 | tr -d '\n')"
printf 'MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD=%s\n' "$(openssl rand -base64 36 | tr -d '\n')"
```

将输出通过 secret manager 或 root-only 流程写入 `/etc/marketplace-server/secrets.env`，不要把示例占位符直接使用：

```text
MARKETPLACE_API_KEY_PEPPER=<generated-base64-pepper>
MARKETPLACE_JWT_SECRET=<generated-jwt-secret>
MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD=<generated-initial-password>
```

```bash
chown root:marketplace-server /etc/marketplace-server/secrets.env
chmod 0640 /etc/marketplace-server/secrets.env
```

Secret 生命周期：

- `MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD` 只在数据库没有任何 User 时创建固定用户名 `admin`。首次成功登录并安全保存新凭据后，可从运行环境删除该变量；重启不会重置已有管理员密码。
- 更换 `MARKETPLACE_JWT_SECRET` 会使现有 management JWT 失效，按计划执行并通知用户重新登录。
- 更换 `MARKETPLACE_API_KEY_PEPPER` 会影响 PAT 校验。没有完成 credential rotation 前不要直接替换或丢失它。
- 数据库中获准可重复查看的 PAT plaintext 使数据库、replica、dump、PITR archive 和备份都进入 credential trust boundary。

## 6. systemd 服务

创建 `/etc/systemd/system/marketplace-server.service`：

```ini
[Unit]
Description=MarketplaceServer
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=marketplace-server
Group=marketplace-server
EnvironmentFile=/etc/marketplace-server/secrets.env
ExecStart=/usr/local/bin/marketplace-server server -c /etc/marketplace-server/config.yaml
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s
KillSignal=SIGTERM
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/marketplace-server/git /var/log/marketplace-server
UMask=0077
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

载入并观察首次启动：

```bash
systemctl daemon-reload
systemctl enable --now marketplace-server
journalctl -u marketplace-server -f
```

启动失败时先检查日志，不要通过删除 database、关闭 migration guard 或放宽 filesystem 权限绕过错误。常见边界包括：

- 未设置或格式错误的 API-key pepper；
- 缺少或显式设置为空的 JWT secret；应用当前只强制非空，强度由 operator 的生成流程保证；
- 空数据库首次启动缺少 bootstrap password；
- PostgreSQL role 缺少 schema DDL 权限；
- Git binary 不可执行；
- Git storage 不可写或路径 containment 检查失败；
- 旧 Identity credential schema 触发 operator rebuild guard。

有价值环境遇到旧 schema guard 时必须先备份并制定显式迁移/PAT rotation；只有可丢弃的开发数据库才能整体重建。

## 7. TLS reverse proxy

应用当前监听 `:8080` 的所有 host interface，不能通过配置改为仅 loopback。必须先用 host firewall/security group 限制 8080 只允许 reverse proxy 来源，外部只开放 TLS 端口；否则客户端可绕过 TLS 直接访问应用。下面是 Nginx 最小基线，域名和证书路径由实际环境管理：

```nginx
server {
    listen 443 ssl http2;
    server_name marketplace.example.com;

    ssl_certificate     /etc/letsencrypt/live/marketplace.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/marketplace.example.com/privkey.pem;

    # Application Git request bodies currently have a fixed 100 MiB limit.
    client_max_body_size 100m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Authorization $http_authorization;

        # Git receive-pack streams request/response bodies. Keep proxy timeouts
        # above the application HTTP limits; Git subprocesses stop after 5m.
        proxy_request_buffering off;
        proxy_buffering off;
        proxy_read_timeout 6m;
        proxy_send_timeout 6m;
    }
}
```

同时将 HTTP 重定向到 HTTPS。不要让 proxy 根据 User-Agent 或自定义 header 绕过认证；`/api/v1`、`/git`、`/distribution` 和 frontend 必须原样交给应用自己的隔离 handler。若 ingress 有请求大小、空闲时间或 WAF 限制，应以真实大 push 测试，而不是默认认为浏览器请求通过就代表 Git transport 可用。

## 8. 首次启动验收

先验证 process 和公开 health endpoint：

```bash
systemctl is-active marketplace-server
curl --fail --silent --show-error https://marketplace.example.com/api/v1/health
```

预期 HTTP `200`，响应 envelope 中 backend 状态为 `ok`。随后按真实用户路径验收：

1. 使用用户名 `admin` 和 bootstrap password 登录 frontend；
2. 创建 PAT，确认 secret 只通过受控界面处理；
3. 创建 Plugin，确认 hidden repository 进入 ready；
4. 使用 Git Basic username + `git-write` PAT push 普通 branch；
5. push 合法 canonical tag，验证候选 Version；
6. 通过管理界面 publish，并验证 clone/fetch 与 distribution；
7. 使用无凭据、错误 scope PAT 和无权限 User 验证 deny path；
8. 重启服务，再次验证数据库状态和 repository 内容没有丢失。

Git remote 形态：

```text
https://marketplace.example.com/git/{namespace}/{plugin}.git
```

Git push 使用 PAT，不使用 management JWT、账户密码或 distribution credential。若提供了无效 credential，服务不会降级为 anonymous。

## 9. 升级与回滚

升级前：

1. 阅读 release notes 与 schema 变化；
2. 停止新写入或安排维护窗口；
3. 对 PostgreSQL 和 Git storage 获取可关联的备份/snapshot；
4. 保存当前 binary、配置版本与 commit ID；
5. 在 staging 使用生产数据副本执行 migration 和真实 Git client 测试。

部署新 binary，并保留明确的上一版本路径：

```bash
systemctl stop marketplace-server
cp --preserve=mode,ownership,timestamps \
  /usr/local/bin/marketplace-server \
  /usr/local/bin/marketplace-server.previous
install -o root -g root -m 0755 marketplace-server /usr/local/bin/marketplace-server.new
mv /usr/local/bin/marketplace-server.new /usr/local/bin/marketplace-server
systemctl start marketplace-server
journalctl -u marketplace-server --since '5 minutes ago'
```

通过 health 和第 8 节的关键读写路径后才能结束维护窗口。数据库 migration 在启动阶段执行，binary 回滚不等于 schema 回滚：

- 若确认没有 schema/data 变化，可停止服务，将 `.previous` 原子替换回正式路径后启动并重新验收；
- 若新版已改变 schema 或接受过写入，保持服务停止，使用该 release 明确支持的恢复流程，或联合恢复升级前 PostgreSQL/Git snapshot，再启动旧 binary；
- 保留失败 binary、日志和 migration 错误供诊断。禁止在线恢复，也禁止只回滚数据库或 Git storage 一侧后继续接受写入。

## 10. 备份、恢复与监控

备份范围、联合快照要求、恢复顺序和完整性检查以 [operations.md](operations.md#backup-与恢复) 为准。部署时必须把 PostgreSQL/PITR、Git filesystem snapshot、secrets/config、binary commit ID 和逻辑时间纳入同一恢复记录；不要在未协调写入时用普通递归复制代替 Git-aware backup 或 filesystem snapshot。

当前没有可由 operator 执行的通用 reconciliation 命令或常驻 recovery worker。恢复扫描发现 pending/manual-required intent、缺失 repository 或 pointer 不一致时，不开放写入；保留现场并使用对应 release 的专用修复程序或人工恢复方案。

至少监控：

- process 存活、启动失败和 restart 次数；
- `/api/v1/health` 的 HTTP 状态；
- PostgreSQL availability、连接池和 storage 使用量；
- Git filesystem 空间、inode、I/O error 和备份新鲜度；
- HTTP 5xx、登录失败、push 拒绝/超时和 pending/manual-required receive intent；
- TLS certificate 有效期。

当前 health endpoint 不应单独作为 readiness 的全部依据。生产上线前必须实际演练一次 PostgreSQL + Git storage 联合恢复，并记录可达到的 RPO/RTO。

## 11. 当前容器资产状态

仓库根目录当前 `Dockerfile`、`docker-compose.yml` 和 `docker-compose-dev.yml` 不是受支持的生产方案：

- 使用浮动 `latest` image/toolchain；
- 没有显式构建 frontend；
- runtime image 的 Git binary 与非 root 运行边界不完整；
- Compose 仍引用不受支持的 MySQL 和不需要的 Redis；
- 未正确声明 PostgreSQL、Git persistent volume、secrets、healthcheck 和 backup。

在这些资产被替换并通过 PostgreSQL、真实 Git push/clone、restart persistence、non-root 和恢复演练前，生产部署使用本文的 binary + systemd 基线。不要把旧 Compose 文件直接用于公网环境。
