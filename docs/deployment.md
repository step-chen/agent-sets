> [中文说明](deployment.zh.md)

# PR Review Automation Deployment Guide

This guide details how to deploy and configure the `pr-review-automation` service.

## 1. Prerequisites

- **Go Runtime**: Go 1.25+ (if deploying via binary)
- **Docker**: Recommended, ensure version 20.10+
- **MCP Servers**: Running Bitbucket, Jira, and Confluence MCP services (esp. those supporting `bitbucket_add_pull_request_comment` tool)
- **LLM API**: A Google Gemini API Key

---

## 2. Environment Configuration

The service is configured via environment variables. Please prepare the following information based on your actual environment:

### Core Configuration

| Variable         | Required | Description              | Example                     |
| :--------------- | :------- | :----------------------- | :-------------------------- |
| `LLM_API_KEY`    | Yes      | Google Gemini API Key    | `AIzaSy...`                 |
| `LLM_ENDPOINT`   | No       | LLM Gateway Address      | `https://ai.example.com/v1` |
| `PORT`           | No       | Service Listening Port   | `8080`                      |
| `WEBHOOK_SECRET` | No       | Bitbucket Webhook Secret | `your-secure-secret`        |

### MCP Service Connection (Bitbucket)

| Variable                 | Required | Description                                        |
| :----------------------- | :------- | :------------------------------------------------- |
| `BITBUCKET_MCP_ENDPOINT` | Yes      | Bitbucket MCP Service URL (SSE) or Command (Stdio) |
| `BITBUCKET_MCP_TOKEN`    | No       | Auth Token for Bitbucket MCP                       |

### MCP Service Connection (Jira/Confluence - Optional)

| Variable                  | Required | Description                    |
| :------------------------ | :------- | :----------------------------- |
| `JIRA_MCP_ENDPOINT`       | No       | Jira MCP Service Address       |
| `CONFLUENCE_MCP_ENDPOINT` | No       | Confluence MCP Service Address |

---

## 3. Configuration File (config.yaml)

In addition to environment variables, the service supports advanced configuration via `config.yaml`.

### Prompts Configuration

| YAML Path     | Description                                  | Default   |
| :------------ | :------------------------------------------- | :-------- |
| `prompts.dir` | Root directory for prompt Markdown templates | `prompts` |

> [!TIP]
> In Docker environments, this directory is mounted at `/app/prompts` by default. If you change this path, ensure you update the volume mount in `docker-compose.yaml`.

---

## 4. Bitbucket Webhook Configuration

To allow the service to receive PR events, please configure the following in your Bitbucket project:

1. Go to Project: **Project Settings** -> **Webhooks** -> **Create webhook**.
2. **Name**: `PR Review Automation`.
3. **URL**: `http://<your-server-ip>:8080/webhook`.
4. **Secret**: Enter the value of the `WEBHOOK_SECRET` environment variable (if set).
5. **Events**:
   - Pull Request: **Opened**, **Modified**, **Rescoped**, **Updated**.
6. **SSL**: SSL verification is recommended for production environments.

---

## 4. Deployment Methods

### A. Using Docker (Recommended)

We provide a multi-stage `Dockerfile` based on `debian:bookworm-slim` to ensure MCP compatibility.

```bash
# Build Image
docker build -t pr-review-automation:latest .

# Run Container
docker run -d \
  -p 8080:8080 \
  --name pr-review \
  -e LLM_API_KEY="xxx" \
  -e BITBUCKET_MCP_ENDPOINT="http://mcp-server:8080" \
  -e WEBHOOK_SECRET="mysecret" \
  pr-review-automation:latest
```

### B. Using Docker Compose

It is recommended to debug this project together with MCP Servers in the same Compose file. The project root provides [docker-compose.example.yaml](../docker-compose.example.yaml) and [.env.example](../.env.example) for direct use.

```bash
cp .env.example .env
# Edit .env to fill in your API Key and connection addresses
docker compose -f docker-compose.example.yaml up -d
```

### C. Binary Deployment

```bash
go mod tidy
CGO_ENABLED=0 go build -o pr-review-server ./cmd/server
./pr-review-server
```

---

## 5. Verification & Troubleshooting

### Health Check

The service has a built-in health check endpoint. You can verify if the service is alive using the following command:

```bash
curl http://localhost:8080/health/ready
# Returns: Ready
```

### View Logs

The service uses structured logging, including PR IDs and processing progress.

```bash
docker logs -f pr-review
```

**Common Connection Issues:**

- `Failed to initialize MCP connections`: Check if the MCP service Endpoint format is correct (SSE mode must start with `http`).
- `Invalid signature`: Check if the Secret entered on the Bitbucket side matches the server's `WEBHOOK_SECRET`.

---

## 6. Security Recommendations

1. **Enable HTTPS**: It is recommended to configure a reverse proxy for the service using Nginx or Traefik and enable TLS.
2. **Least Privilege**: It is recommended to configure Read-Only + Comment permissions for the Bitbucket MCP token to avoid excessive authorization.
3. **Intranet Deployment**: Considering that code and Jira data are involved, it is recommended to deploy the service in an intranet environment.
4. **File Permissions**: Ensure that the `.env` file is only visible to the user running the service to prevent sensitive information leakage:
   ```bash
   chmod 600 .env
   ```
