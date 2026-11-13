<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset=".github/assets/logo.svg">
    <img src=".github/assets/logo.svg" alt="dropsite" width="300">
  </picture>
</h1>

[English](README.md) | [Русский](README.ru.md) | **简体中文**

dropsite 在您自己的服务器上发布网站。您对 ChatGPT 或 Claude 说“为我的咖啡店做一个网站并发布”，大约一分钟后，网站就会通过 HTTPS 在您自己的域名下上线。修改内容、查看表单提交、访问量和设置密码，也只需在同一个对话中提出请求。

dropsite 只托管已经做好的静态网站（HTML、CSS、JavaScript、图片），不负责构建项目，也不运行服务器端代码。

## 准备工作

- **一台租用的服务器（VPS）**：Ubuntu 22.04、Ubuntu 24.04 或 Debian 12，内存 1 GB 及以上。服务商会提供服务器的 IP 地址和 root 密码（或允许您添加 SSH 密钥）。
- **一个域名**，且不用于其他任何服务，例如 `sites.example.com`。
- **ChatGPT 或 Claude**，用对话来制作网站。不使用对话时，也可以在网页管理界面中上传网站。

## 一条命令完成安装

请在**您自己的电脑**上运行，而不是在服务器上。macOS 和 Linux 请打开“终端”应用；Windows 请使用 WSL（Microsoft Store 中的 Ubuntu）或 Git Bash。

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install-server.sh | sh
```

安装程序会询问服务器 IP、root 密码（或密钥文件）、服务器的简短名称、域名和电子邮箱；提问语言会根据系统语言自动选择中文、英文或俄文。随后它会：

1. 为这台服务器单独创建一个密钥，因此密码只使用一次，不会被保存；
2. 在服务器上安装 Docker 和 dropsite，只开放 22、80、443 端口，开启每日自动备份并启动所有服务（约 10–20 分钟）；
3. 在您电脑的 `~/.ssh/config` 中添加这台服务器，之后运行 `ssh <名称>` 即可连接，并打开通往管理界面的私有隧道；
4. 告诉您需要在域名注册商处添加哪些 DNS 记录：`@` 和 `*` 两条 `A` 记录，值为服务器 IP（服务器有 IPv6 时还需添加 `AAAA` 记录），并等待域名生效；
5. 显示管理员账号、自动生成的密码，以及连接 ChatGPT 或 Claude 的方法。

如果中途出错，再次运行同一条命令即可，已完成的步骤会自动跳过。详细说明和参数：[docs/install.md](docs/install.md#one-command-installer)。

## 开始使用

1. 运行 `ssh <名称>`，保持窗口打开，在浏览器中访问 `http://localhost:<端口>`（端口会在安装结束时显示）。使用您的邮箱和生成的密码登录。
2. **入门指南**页面是一份会自动检查服务器的清单：域名是否指向本服务器、HTTPS 是否正常、是否已连接对话、是否已有第一个网站。它会列出确切的 DNS 记录并提供复制按钮，某一步未通过时会说明如何解决。可选步骤：邀请同事，以及为表单提交配置 Telegram 或电子邮件。
3. 打开 **连接 AI** 页面，选择您使用的对话工具并按编号步骤操作：把地址 `https://api.<您的域名>/mcp` 添加为连接器，然后点击 **连接**，在打开的 dropsite 页面中输入一次性连接码。对话中永远不会要求您输入密码。
4. 对对话说：“为我的咖啡店做一个网站并发布”。

## 功能一览

### 用对话制作和修改网站

- **在对话中发布。** ChatGPT、claude.ai、Claude Desktop 和 Claude Code 会制作网站、发布并回复可用的链接。网站完全自包含（自带图片和字体，不依赖第三方 CDN），优先适配手机，加载迅速。
- **在任意新对话中修改网站。** 助手会从服务器读取已发布的文件，只修改您要求的部分，然后发布一个新版本。
- **不用对话也能发布。** 在网页管理界面中把 zip 或 tar.gz 拖到网站上，运行 `dropsite deploy ./文件夹`，或调用 HTTP API。
- **给出您喜欢的网站。** 提供链接或图片，就能得到风格相似的原创网站：配色、字体、间距和布局相近，但绝不会复制对方的文字、图片或标志（[docs/references.md](docs/references.md)）。
- **仅在您要求时使用模板。** 五个起步网站（咖啡馆、服务、活动、作品集、链接页），提供英文或俄文内容；其他情况下，助手会为每个网站从头设计（[docs/screenshots.md](docs/screenshots.md#templates)）。
- **复制网站**，在不影响原网站的情况下尝试另一种方案。

### 放心发布

- **草稿与预览。** 把新版本保存为草稿，在网站的私有预览地址中检查；在您点击**发布**之前，访客看到的仍是当前版本。对于已上线的网站，助手会先保存草稿，并在发布前征求您的同意。
- **定时发布。** 现在准备好版本，让它在指定时间自动上线，最长可提前一年。
- **版本与回滚。** 每次发布都是一个独立版本。回到以前的版本即时完成，访客永远不会看到更新到一半的网站。
- **质量报告。** 每次发布都会得到一个评分，并列出失效链接、缺失文件、从其他服务器加载的文件、缺少 `alt` 的图片、缺少标题或描述的页面，以及体积最大的页面（[docs/quality.md](docs/quality.md)）。
- **自动优化。** 为支持的浏览器提供 JPEG 和 PNG 图片的 WebP 版本；网站没有 `sitemap.xml`、`robots.txt` 和网站图标时自动补上。您自己的文件不会被改动（[docs/publishing.md](docs/publishing.md)）。
- **截图和前后对比。** 启用可选的渲染服务后，助手可以查看自己做的网站在电脑和手机上的效果，并对比两个版本（[docs/screenshots.md](docs/screenshots.md)）。

### 收集表单提交，内容实时更新

- **无需第三方服务的表单。** 表单提交保存在您的服务器上，并发送到 Telegram、电子邮件或带签名的 HTTPS webhook，失败时会自动重试。防机器人措施：隐藏的蜜罐字段、频率限制，以及可选的自托管 Altcha 验证。可以在网站页面中查看提交并导出为 CSV（[docs/forms.md](docs/forms.md)）。
- **可在对话中编辑的网站数据。** 菜单、价格、商品目录、日程或评价保存在数据集合中，由页面加载显示。说一句“把卡布奇诺的价格改成 25”，修改立即生效，无需发布新版本。数据集合可以是公开的（只有您能写入），也可以是需审核的（访客提交条目，经您批准后显示）。内置数据集合模板：菜单、商品目录、服务、活动、团队、常见问题、评价。
- **Google 表格。** 把表单提交写入电子表格，或从表格填充数据集合，可手动或定时同步（[docs/sheets.md](docs/sheets.md)）。
- **API 密钥始终保密。** 页面可以通过您的服务器调用第三方 API（OpenAI、CRM、Telegram 机器人），由服务器附加密钥。密钥只能在网页管理界面中填写，对话和 API 既不接收也不显示密钥（[docs/proxy.md](docs/proxy.md)）。

### 控制谁能看到什么

- **密码保护的网站**，也可以对搜索引擎隐藏，或设置最后一天，到期后网站自动关闭（[docs/site-access.md](docs/site-access.md)）。
- **使用您自己的域名。** 除了基础域名下的地址，网站还可以在 `promo.example.com` 这样的域名上打开；助手会告诉您要添加哪些 DNS 记录，管理员批准后域名即可使用（[docs/external-domains.md](docs/external-domains.md)）。
- **团队共享工作区。** 创建工作区并添加同事，每位成员都可以管理其中的网站。除此之外，每个用户只能看到自己的网站。
- **无需脚本的访问统计。** 根据服务器日志统计每天的页面浏览量和访客数、热门页面和来源。如需更多数据，可按编号添加 Yandex Metrica 或 Google Analytics 4 统计代码（[docs/statistics.md](docs/statistics.md)）。

### 稳定可靠地运行

- **操作日志。** 管理员可以看到登录、发布、回滚，以及用户、域名、表单和设置的变更。
- **三种界面语言：** 简体中文、英文和俄文。
- **预构建镜像和带版本号的发布**，支持 amd64 和 arm64，并附有签名的构建来源证明；服务器上无需编译（[docs/releasing.md](docs/releasing.md)）。
- **备份，一条命令更新和回滚：** 每日备份、`dropsite-update`、`dropsite-rollback`。
- **网站持续可用**，即使管理服务已停止或正在更新。

## 详细说明

### 系统要求

- Linux 服务器，amd64 或 arm64；Git、Docker Engine 24+ 和 Compose 2.24.4+（一键安装程序会自动安装）。
- 开放 80 和 443 端口（TCP），以及用于 HTTP/3 的 443/UDP。管理端口 8082 必须对互联网保持关闭。
- 2 vCPU、4 GB 内存可支撑最多 10 000 个网站和 1000 个用户；运行预构建镜像只需 1 GB 内存。在 1 CPU、1 GB 内存的服务器上从源码构建需要 2 GB swap（[构建内存](docs/install.md#build-memory)）。
- 专用的基础域名。已发布的网站会在其子域名上运行任意 JavaScript：请勿在该域名上放置公司服务、登录页面或 cookie。对于有不受信任用户的公开安装，建议将域名提交到 [Public Suffix List](https://publicsuffix.org/)。

工作原理：两个容器。**Caddy** 按主机名提供网站并申请 HTTPS 证书，其配置是静态的。**control**（一个 Go 程序）负责数据库、账户、API、MCP 服务和网页管理界面，通过原子切换 Caddy 读取的链接来发布版本。

### 安装方式

**一键安装程序**（见上文）。所有问题都可以通过参数传入以实现自动化（`--host`、`--name`、`--domain`、`--email`、`--key`、`--yes` 等），`--check-dns --name <名称>` 可在之后检查 DNS：[docs/install.md](docs/install.md#one-command-installer)。安装程序以 `root` 身份登录；暂不支持只提供 sudo 用户的服务商。

**使用 Docker Compose 手动安装。** 为 `example.site` 和 `*.example.site` 添加指向服务器的 `A`/`AAAA` 记录（`*` 记录同时覆盖 `api.example.site`），然后执行：

```bash
git clone https://github.com/b0balYson/dropsite.git
cd dropsite
# 最新的发布标签：compose 文件和 Caddyfile 与镜像属于同一版本。
git checkout "$(git tag -l 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1)"
cd deploy
cp .env.example .env
chmod 600 .env
# 填写 BASE_DOMAIN、ACME_EMAIL、ADMIN_EMAIL、ADMIN_PASSWORD
nano .env
docker compose config --quiet
docker compose pull
docker compose up -d --wait
docker compose ps
```

`control` 应为 `healthy`，`caddy` 应为 `running`；失败时请查看 `docker compose logs --tail=100 control caddy`。网页管理界面只监听 `127.0.0.1:8082`：请通过 `ssh -L 8082:127.0.0.1:8082 you@server` 访问。团队使用时，可以通过 `ADMIN_BIND_IP` 和 `UI_URL` 把它绑定到服务器在私有覆盖网络（Tailscale、WireGuard、NetBird）中的地址。更多内容：[docs/install.md](docs/install.md)、[docs/production-checklist.md](docs/production-checklist.md)。

**版本。** 每个发布版本都是一个 Git 标签 `vX.Y.Z`，并提供镜像 `ghcr.io/b0balyson/dropsite-control` 和 `ghcr.io/b0balyson/dropsite-caddy`。`.env` 中的 `DROPSITE_VERSION=latest`（默认）会跟随新版本；设为 `X.Y.Z` 则固定在某个版本。没有发布版本或需要运行自己的修改时，可从源码构建：`docker compose up -d --build --wait`（[从源码构建](docs/install.md#building-from-source)、[docs/releasing.md](docs/releasing.md)）。

**HTTPS 证书。** 默认模式（`TLS_MODE=ondemand`）下，每个地址在首次访问时获得自己的 Let's Encrypt 证书。Let's Encrypt 对每个注册域名**每 7 天最多签发 50 张新证书**；一个同时有随机地址和易读地址的网站会用掉两张，因此每周大约可以新增 **25 个网站**。网站的草稿预览地址还会额外用掉一张，且只用一次。续期不计入限额。超出时请切换到**通配符模式**（`TLS_MODE=wildcard`，`compose.wildcard.yml`）：通过 DNS-01 为 `*.<BASE_DOMAIN>` 申请一张证书，新网站不再消耗证书；需要 DNS 托管在 Cloudflare，并提供具有 `Zone.DNS:Edit` 权限的 API 令牌，此模式下每个版本还有自己的预览地址。分步说明：[docs/tls.md](docs/tls.md)。所有签发的证书都会出现在公开的证书透明度（Certificate Transparency）日志中，因此网站地址并不保密。

### 连接 AI 客户端

| 客户端 | 方法 |
| --- | --- |
| ChatGPT | Settings → Apps → Advanced settings → Developer mode，然后用地址 `https://api.<BASE_DOMAIN>/mcp` 和 OAuth 创建应用。并非所有套餐都支持自定义应用，请查看 [help.openai.com](https://help.openai.com/)。 |
| claude.ai、Claude Desktop | Settings → Connectors → Add custom connector，粘贴地址，点击 Connect。 |
| Claude Code | `claude mcp add --transport http dropsite https://api.<BASE_DOMAIN>/mcp`，然后在 Claude Code 中输入 `/mcp` 登录。 |
| 本机上的 Claude Code、Codex、Cursor、Claude Desktop | 在 **连接 AI** 页面点击 **连接**，会显示一条命令：它会安装 CLI、登录并运行 `dropsite setup`（安装发布技能，或为 Claude Desktop 配置本地 MCP 服务）。 |

连接码 10 分钟内有效，且只能使用一次。通过对话，即使是管理员也只能管理自己的网站；安装级设置只能在网页管理界面中修改，对话也不会创建令牌或连接码。已连接的应用会列在 **连接 AI** 页面上，并可在那里断开。工具、规则和限制：[docs/ai-clients.md](docs/ai-clients.md)、[docs/mcp-tools.md](docs/mcp-tools.md)。

### CLI

安装脚本会从 GitHub Releases 下载程序并校验 SHA-256（`DROPSITE_VERSION=X.Y.Z` 可指定版本）：

```bash
curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.sh | DROPSITE_SERVER=https://api.example.com DROPSITE_CODE=ABCD-EFGH-JKMNP sh
```

```powershell
$env:DROPSITE_SERVER='https://api.example.com'; $env:DROPSITE_CODE='ABCD-EFGH-JKMNP'; irm https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install.ps1 | iex
```

命令：`login`、`setup`（`--remove` 撤销）、`deploy`（`--draft`、`--expires 7d`）、`sites`、`deploys`、`rollback`、`draft publish|discard`、`clone`、`domains`、`workspaces`、`delete`、`mcp`、`version`。第一次运行 `dropsite deploy ./dist` 会创建网站并记录在 `.dropsite.json` 中，之后的运行会更新同一个网站。包含疑似机密文件（`.env*`、密钥）的文件夹会被拒绝发布，除非使用 `--skip-sensitive` 或 `--include-sensitive`。CLI 与服务器的次版本号不一致时，会给出一行提示。没有发布版本时，可[从源码构建 CLI](docs/install.md#cli-from-source)。

### HTTP API

`https://api.<BASE_DOMAIN>/api/v1`，使用在网页管理界面中创建的 Bearer 令牌；OpenAPI 文档位于 `/api/v1/openapi.json`。接口约定：[docs/api-contract.md](docs/api-contract.md)。

网站压缩包的根目录需要有 `index.html`（单个顶层文件夹如 `dist/` 会被自动展开）；默认解压后最多 50 MB，单个文件最多 20 MB，最多 2000 个文件。`/about` 会返回 `about.html`，`404.html` 是“页面不存在”页面，`200.html` 会启用单页应用模式。`/_form/` 和 `/_dropsite/` 路径由平台保留。

### 配置

配置保存在 `deploy/.env` 中（[.env.example](deploy/.env.example)）。

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `BASE_DOMAIN` | 必填 | 网站的基础域名 |
| `ACME_EMAIL` | 必填 | 证书账户的电子邮箱 |
| `ADMIN_EMAIL`、`ADMIN_PASSWORD` | 必填 | 第一位管理员；密码只在首次启动时读取 |
| `DROPSITE_VERSION` | `latest` | 运行的发布镜像，或 `X.Y.Z` |
| `ADMIN_BIND_IP`、`UI_URL` | `127.0.0.1`、`http://<ADMIN_BIND_IP>:8082` | 网页管理界面的监听地址，以及邀请链接中的地址 |
| `API_HOST` | `api.<BASE_DOMAIN>` | API 和 MCP 的主机名 |
| `TLS_MODE`、`CF_API_TOKEN` | `ondemand` | 证书模式；令牌仅用于通配符模式 |
| `MAX_SITE_BYTES`、`MAX_FILE_BYTES`、`MAX_FILES`、`MAX_SITES_PER_USER`、`KEEP_RELEASES` | 50 MB、20 MB、2000、10、5 | 限额；管理员可在“设置”中全局覆盖，也可按用户设置 |
| `MAX_DOMAINS_PER_SITE` | `5` | 每个网站可绑定的自有域名数 |
| `SERVER_IPS` | `BASE_DOMAIN` 的地址 | 作为 DNS 目标显示的公网 IP |
| `PUBLISH_OPTIMIZE` | `on` | 发布时的自动优化 |
| `COMPOSE_PROFILES`、`RENDERER_URL` | 关闭 | 可选的截图渲染服务（[docs/screenshots.md](docs/screenshots.md)） |

其他变量：`RESERVED_SLUGS`（额外禁止使用的短地址）、`LOG_LEVEL`、`STATS_INTERVAL` 和 `STATS_LOG_FILE`（设为 `off` 关闭统计）、`TELEGRAM_API_URL`、`FORM_WEBHOOK_ALLOW_CIDRS`（仅用于测试环境）。Telegram 机器人、邮件服务器、Google 服务账号和限额由管理员在网页管理界面的**设置**中配置。

### 运维

| 任务 | 通过安装程序部署的服务器 | 手动操作（在 `dropsite/deploy` 中执行） |
| --- | --- | --- |
| 更新 | `ssh <名称> dropsite-update` | 备份，切换到新标签，`docker compose pull && docker compose up -d --wait` |
| 撤销上次更新 | `ssh <名称> dropsite-rollback` | 切回之前的标签，并恢复更新前的备份 |
| 备份 | 每天自动备份，或 `ssh <名称> dropsite-backup` | `docker compose exec -T control /control backup - > backup.tar.gz` |
| 重置管理员密码 | `ssh <名称> dropsite-admin-password` | `docker compose exec -T control /control reset-password [email]` |
| 日志 | `docker compose logs -f` | 同左 |

数据库迁移会自动执行；不支持降级，因此回滚时需要配合备份。表单密钥文件 `state/secrets.key` 不包含在备份中，请另外保存一份。切勿运行 `docker compose down -v`，它会删除数据。详细说明：[docs/backup.md](docs/backup.md)、[docs/releasing.md](docs/releasing.md#on-a-server)。

### 安全说明

- 网页管理界面运行在独立的监听端口上，无法从互联网访问；密码只在这里输入。
- 对话客户端通过 OAuth 2.1（PKCE、短期令牌、刷新令牌轮换）连接，并用一次性连接码确认。API 只接受 Bearer 令牌，令牌以哈希形式存储。
- 访问其他用户的网站时始终返回“未找到”。压缩包在写入磁盘前会经过检查，文件中包含机密密钥的发布会被拒绝。
- Caddy 以只读方式读取网站文件，无法访问数据库。两个容器都以非 root 用户运行，根文件系统为只读。
- 请使用专用的基础域名（见“系统要求”）。
- 除非明确允许，管理界面不会在公网地址上启动；安装完成后，服务器上不再保存首个管理员的密码。
- 威胁模型、CDN 配置和滥用处理：[docs/security.md](docs/security.md)（英文）。

### 开发

```bash
make tools        # 把 Go 和 golangci-lint 安装到 .tools/（不影响系统）
make test lint
scripts/dev-control.sh   # 直接运行 control，管理界面在 http://127.0.0.1:18082
make dev-up       # 在 Docker 中运行完整环境并使用本地 CA：https://<site>.dropsite.localhost:18443
make e2e
make release-check
```

更新记录见 [CHANGELOG.md](CHANGELOG.md)。

### 许可证

[Apache-2.0](LICENSE)
