> [Read in English](deployment.md)

# PR Review Automation 部署指南

本指南详细介绍了如何部署和配置 `pr-review-automation` 服务。

## 1. 前置条件

- **Go 运行时**: Go 1.25+ (如果使用二进制部署)
- **Docker**: 推荐使用，确保版本 20.10+
- **MCP Servers**: 需要已运行的 Bitbucket、Jira 和 Confluence MCP 服务 (特别是支持 `bitbucket_add_pull_request_comment` 工具的项目)
- **LLM API**: 拥有 Google Gemini 的 API Key

---

## 2. 环境变量配置

服务通过环境变量进行配置。请根据实际环境准备以下信息：

### 核心配置

| 变量名           | 必填 | 说明                       | 示例                        |
| :--------------- | :--- | :------------------------- | :-------------------------- |
| `LLM_API_KEY`    | 是   | Google Gemini API 密钥     | `AIzaSy...`                 |
| `LLM_ENDPOINT`   | 否   | LLM 网关地址               | `https://ai.example.com/v1` |
| `PORT`           | 否   | 服务监听端口               | `8080`                      |
| `WEBHOOK_SECRET` | 否   | Bitbucket Webhook 签名密钥 | `your-secure-secret`        |

### MCP 服务连接 (Bitbucket)

| 变量名                   | 必填 | 说明                                          |
| :----------------------- | :--- | :-------------------------------------------- |
| `BITBUCKET_MCP_ENDPOINT` | 是   | Bitbucket MCP 服务的 URL (SSE) 或命令 (Stdio) |
| `BITBUCKET_MCP_TOKEN`    | 否   | 访问 Bitbucket MCP 的鉴权令牌                 |

### MCP 服务连接 (Jira/Confluence - 选填)

| 变量名                    | 必填 | 说明                    |
| :------------------------ | :--- | :---------------------- |
| `JIRA_MCP_ENDPOINT`       | 否   | Jira MCP 服务地址       |
| `CONFLUENCE_MCP_ENDPOINT` | 否   | Confluence MCP 服务地址 |

---

## 3. 配置文件配置 (config.yaml)

除了环境变量，服务还支持通过 `config.yaml` 进行深度配置。

### Prompts 解析配置

| YAML 路径     | 说明                             | 默认值    |
| :------------ | :------------------------------- | :-------- |
| `prompts.dir` | 存放提示词 Markdown 文件的根目录 | `prompts` |

> [!TIP]
> 在 Docker 环境中，该目录默认挂载在 `/app/prompts`。如果您修改了此路径，请确保同步更新 `docker-compose.yaml` 中的挂载点。

---

## 4. Bitbucket Webhook 配置

为了让服务接收 PR 事件，请在 Bitbucket 项目中进行如下设置：

1. 进入项目：**Project Settings** -> **Webhooks** -> **Create webhook**。
2. **Name**: `PR Review Automation`。
3. **URL**: `http://<your-server-ip>:8080/webhook`。
4. **Secret**: 填写 `WEBHOOK_SECRET` 环境变量的值（如果已设置）。
5. **Events**:
   - Pull Request: **Opened**, **Modified**, **Rescoped**, **Updated**。
6. **SSL**: 建议在生产环境启用 SSL 验证。

---

## 4. 部署方式

### A. 使用 Docker (推荐)

我们提供了多阶段构建的 Dockerfile，基于 `debian:bookworm-slim` 以确保 MCP 兼容性。

```bash
# 构建镜像
docker build -t pr-review-automation:latest .

# 运行镜像
docker run -d \
  -p 8080:8080 \
  --name pr-review \
  -e LLM_API_KEY="xxx" \
  -e BITBUCKET_MCP_ENDPOINT="http://mcp-server:8080" \
  -e WEBHOOK_SECRET="mysecret" \
  pr-review-automation:latest
```

### B. 使用 Docker Compose

推荐将本项目与 MCP Servers 放在同一个 Compose 文件中联调。项目根目录提供了 [docker-compose.example.yaml](../docker-compose.example.yaml) 和 [.env.example](../.env.example) 供直接使用。

```bash
cp .env.example .env
# 编辑 .env 填充你的 API Key 和连接地址
docker compose -f docker-compose.example.yaml up -d
```

### C. 二进制部署

```bash
go mod tidy
CGO_ENABLED=0 go build -o pr-review-server ./cmd/server
./pr-review-server
```

---

## 5. 验证与排查

### 健康检查

服务内置了健康检查端点，可以通过以下命令验证服务是否存活：

```bash
curl http://localhost:8080/health/ready
# 返回: Ready
```

### 查看日志

服务使用结构化日志输出，包含 PR ID 和处理进度。

```bash
docker logs -f pr-review
```

**常见连接问题：**

- `Failed to initialize MCP connections`: 检查 MCP 服务的 Endpoint 格式是否正确（SSE 模式必须以 `http` 开头）。
- `Invalid signature`: 检查 Bitbucket 侧填写的 Secret 是否与服务端的 `WEBHOOK_SECRET` 一致。

---

## 6. 安全建议

1. **启用 HTTPS**: 建议通过 Nginx 或 Traefik 为服务配置反向代理并启用 TLS。
2. **最小权限**: 建议为 Bitbucket MCP 令牌配置只读 + 评论权限，避免过度授权。
3. **内网部署**: 考虑到涉及代码和 Jira 数据，建议将服务部署在内网环境。
4. **文件权限**: 确保 `.env` 文件仅对运行服务的用户可见，防止敏感信息泄露：
   ```bash
   chmod 600 .env
   ```
