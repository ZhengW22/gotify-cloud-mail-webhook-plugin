# Gotify Cloud Mail Webhook Plugin

**中文** | [English](README.en.md)

专门接收 [cloud-mail](https://github.com/maillab/cloud-mail) 现有邮件 Webhook 的 Gotify 原生插件。**不修改 cloud-mail，不运行额外服务，不需要 Gotify 应用 Token。**插件直接在 Gotify 进程中接收请求并发送通知。

## 功能

- 邮件主题作为通知标题，显示发件人、收件人、验证码及邮件时间。
- 优先显示 `text`；没有纯文本时解析 `content` 中的 HTML，转换为可读文本，保留普通 HTTP/HTTPS 链接。
- 忽略 HTML 的脚本、样式和图片，不执行脚本、不访问外链，不保留邮件的完整排版。
- 兼容 cloud-mail 的可选“密钥”：校验原样发送的 `Authorization` 请求头。
- 正文长度可配置，默认最多 6000 个 Unicode 字符（包括邮件信息）；超出会明确标记截断。
- 单次请求最大 8 MiB。收到合法请求且提交 Gotify 成功才返回 200；失败返回非 2xx，供 cloud-mail 重试。
- 按正整数 `emailId` 对同一插件实例的成功请求去重：内存保留 24 小时、最多 1024 条；重启会清空，不能保证跨重启恰好一次。一个插件实例建议连接一个 cloud-mail 实例。

## 版本与架构

当前针对官方 Docker 镜像 **`gotify/server:3.1.1`**，使用官方的 **Go 1.26.0** 构建环境及该版本的共享依赖版本。

Go 原生插件对工具链、共享依赖、架构和构建选项敏感。不要把本机其他 Go 版本编出的 `.so` 直接用于官方镜像，也不要假定升级 `latest` 后旧插件仍可加载。建议固定 Gotify 镜像版本。

`compatibility/server.go.mod` 和 `compatibility/GO_VERSION` 是 Gotify v3.1.1 的官方源文件快照，方便核对依赖。项目 `go.mod` 的版本由该依赖图裁剪而来。

构建脚本还保持官方使用的 `/go/pkg/mod` 依赖路径；Go 插件的包指纹可能受源码路径影响，仅版本相同仍可能加载失败。下载缓存保存在本项目的 `.build-cache/`，不会提交到 Git。

支持构建：

| 服务器 | 构建命令 |
| --- | --- |
| ARM64 / aarch64 | `make build-arm64` |
| 32 位 ARMv7 / armv7l | `make build-arm7` |
| x86_64 | `make build-amd64` |

在运行 Gotify 的服务器执行 `uname -m` 可区分 ARM64 和 ARMv7；也要确认容器实际运行的镜像架构。

构建脚本在 **amd64 构建机**使用 Gotify 官方跨架构构建镜像，并带上插件所需的 CGO 环境。它不是在 ARM 容器内运行构建；在 ARM 主机直接执行，需要具备 amd64 仿真能力。可以在 amd64 电脑构建后把 `.so` 复制到服务器，或将仓库上传 GitHub 后手动运行 **Build plugins** 工作流，从 Artifacts 下载对应架构文件。

```bash
make test
make vet
make build-arm64
```

输出例如：

```text
build/cloud-mail-webhook-gotify-v3.1.1-linux-arm64.so
```

升级 Gotify 时，先核对新版本的 `GO_VERSION`、`go.mod` 及官方 Dockerfile，更新快照、项目依赖及构建版本，再重新构建和验证。参考 [官方插件构建说明](https://gotify.net/docs/plugin-deploy)。

## GitHub Actions 自动发布

**Build plugins** 工作流支持两种发布方式：

- **推送新标签**：推送 `v0.1.1` 这类版本标签后，自动测试并构建 amd64、ARM64、ARMv7 插件，然后创建 GitHub Release，附带三个 `.so` 文件和 `SHA256SUMS`。
- **补发已有标签**：在 Actions → Build plugins → Run workflow 中选择包含新工作流的 `main` 分支，`tag` 填写已有标签（例如 `v0.1.0`）。留空只构建 Artifacts，不发布 Release。

```bash
git tag -a v0.1.1 -m "Release v0.1.1"
git push origin v0.1.1
```

所有测试和三个架构构建通过后才发布；amd64 额外进行官方 Gotify 容器端到端测试。目前没有 ARM 运行验证。带预发布后缀的标签（例如 `v0.1.1-rc.1`）会发布为 prerelease。

手动补发使用该标签对应的源码提交，而不是直接用最新 `main` 的源码。重复运行会保留已公开的 Release；上传中断留下的草稿可以重试完成。无需额外配置个人访问 Token，发布任务使用 GitHub 内置令牌的 `contents: write` 权限。

## Docker 安装

在现有 Gotify Compose 文件旁创建 `plugins` 目录，只把**一个匹配架构的 `.so`**放进去。校验文件、README、压缩包和其他架构插件不要放进插件目录，Gotify 会尝试加载目录内的文件。

对现有 Gotify 服务增加以下配置，原有数据库和数据卷保持原样：

```yaml
services:
  gotify:
    image: gotify/server:3.1.1
    environment:
      GOTIFY_PLUGINSDIR: /app/plugins
    volumes:
      # 保留你原来的 /app/data 挂载，不要替换它。
      - ./plugins:/app/plugins:ro
```

这里是配置片段，需合并到现有服务；不要覆盖其他环境变量及 volumes。SELinux 主机可能需要给挂载目录设置合适标签（例如单容器使用 `:ro,Z`）。

`compose.example.yml` 是**全新实例**示例，不用于覆盖现有 Gotify 数据目录。

更新配置后重新创建 Gotify 容器（服务名按实际修改）：

```bash
docker compose up -d gotify
docker compose logs --tail=100 gotify
```

如果日志出现 `built with a different version`，通常是 Go 或依赖版本不匹配；如果是 ELF/架构错误，核对 `.so` 与镜像架构。

## 配置与 cloud-mail 接入

1. 登录 Gotify，进入插件页面，找到 **Cloud Mail Webhook**。
2. 查看并保存插件配置，然后启用。插件默认生成随机密钥；可以自行设置至少 16 字节的随机值。

```yaml
secret: "replace-with-a-random-secret"
priority: 5
max_body_chars: 6000
```

`secret` 使用不含空格的可见 ASCII 字符，建议保留自动生成的随机值。`priority` 取值 0–10，`max_body_chars` 取值 100–20000。

3. 插件说明页会显示当前用户专属的接收地址，例如：

```text
https://gotify.example.com/plugin/<插件ID>/custom/<插件Token>/receive
```

若 Gotify 部署在子路径下，检查说明页生成的地址与实际反向代理路径一致。

4. 在 **cloud-mail → 系统设置 → Webhook** 中填写：

| 项目 | 值 |
| --- | --- |
| 开关 | 启用 |
| 请求地址 | 插件说明页中的完整 HTTPS 接收地址 |
| 密钥 | 插件配置中的 `secret`，原样填写，**不要添加 `Bearer `** |
| 重试次数 | 建议 2 |

不需要在 URL 中放密钥、不需要改邮件项目、不需要选择 Gotify 应用。每个 Gotify 用户的插件实例会把消息交给其自己的插件应用。

**如果 Gotify 域名受 Cloudflare Access 邮箱验证码保护，请为这个接收端点配置合适的机器访问规则。**现有 cloud-mail Webhook 不携带 Access Service Token 的两个专用请求头；仅复制地址不能通过邮箱验证码登录。

## 验证

给 cloud-mail 发一封包含主题、发件人和验证码的邮件；再发一封只有 HTML 正文的邮件。在 Gotify 检查两条通知。

也可以直接测试插件接口。以下命令会交互读取密钥，不在命令行写死；地址中 `<插件ID>`、`<插件Token>` 需替换，推荐直接复制插件说明页的地址：

```bash
read -rsp 'Webhook secret: ' MAIL_WEBHOOK_SECRET
echo
printf 'Authorization: %s\n' "$MAIL_WEBHOOK_SECRET" | \
  curl --fail-with-body --header @- \
    --header 'Content-Type: application/json' \
    --data '{"emailId":10001,"sendEmail":"sender@example.com","sendName":"测试发件人","toEmail":"me@example.com","subject":"HTML 邮件测试","text":"","content":"<h1>你好</h1><p>验证码：<b>123456</b></p>","code":"123456"}' \
    'https://gotify.example.com/plugin/<插件ID>/custom/<插件Token>/receive'
unset MAIL_WEBHOOK_SECRET
```

成功响应：`{"status":"success"}`。重复发送同一正整数 `emailId` 返回 `{"status":"duplicate"}`；换一个 ID 可重新测试。未提供 `emailId` 或为 0 时不去重。

错误码：401 密钥不正确，400 JSON/邮件数据无效，413 请求过大，415 Content-Type 不正确，503 插件未启用或消息处理器未就绪，502 提交消息失败。

## 开发验证

```bash
go test -race ./...
go vet ./...
```

官方容器端到端验证（在 amd64 构建机运行）：

```bash
make build-amd64
python3 scripts/smoke.py
```

脚本会创建临时 Gotify 3.1.1 容器，验证插件加载、认证、纯文本/HTML 邮件、发件人和收件人显示，以及重试去重。只向本机临时容器发测试消息，结束后删除该容器；不连接现有 Gotify，也不修改现有数据。需要 Docker 权限，Python 仅用于此开发验证。

测试覆盖现有 Webhook 字段、认证、失败重试、并发去重、非法与超大请求、纯文本优先、HTML 转文本和中文截断。开发环境 Go 版本通过不代表官方镜像可以加载；最终仍需用官方构建环境生成 `.so` 并做容器加载验证。

当前实现已通过 `go test -race -cover ./...`（语句覆盖率 91.5%）、`go vet ./...`，并使用 Go 1.26.0 编译 amd64 插件，在官方 `gotify/server:3.1.1` 中通过上述端到端测试。ARM64/ARMv7 已提供构建脚本及工作流，但尚未在 ARM 设备上运行验证。

Gotify 原生插件仅适用于支持 Go plugin 的系统；本项目的分发目标为 Linux。插件本身不需要开放新端口、独立容器或额外数据库。
