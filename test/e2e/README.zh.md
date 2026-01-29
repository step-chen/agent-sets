# End-to-End (E2E) 测试指南

[English Version](./README.md)

本目录包含 PR 评审自动化系统的端到端测试套件。该测试通过模拟真实的 Webhook 请求、执行真实的 LLM 推理和获取真实的 MCP 数据来验证系统的完整链路。

## 目录结构

```text
test/e2e/
├── requests/           # 存放 Webhook Payload (JSON)
│   ├── 01_pr.json
│   └── 02_chunked.json
├── config.test.yaml    # E2E 专用配置文件
├── .env                # E2E 专用环境变量 (API Key 等)
├── README.md           # 英文文档
├── README.zh.md        # 本文档
└── e2e_main_test.go    # 测试运行器
```

## 测试工作原理

1.  **文件驱动**：测试器按字母顺序读取 `requests/` 目录下的所有 `*.json` 文件。
2.  **全链路模拟**：每个 JSON 文件都会模拟一个发送到 `/webhook` 端点的 POST 请求，触发完整的处理流（Debounce -> WorkerPool -> PR Review Pipeline）。
3.  **真实交互**：
    - **LLM**：执行真实的在线模型请求。
    - **MCP**：执行真实的读操作（获取 Diff、获取文件内容）。
4.  **安全拦截**：通过 `InterceptingTransport` 拦截 `bitbucket_add_pull_request_comment` 工具调用，防止测试将评论真实发布到 Bitbucket。
5.  **统一报告**：测试完成后，会在控制台汇总打印所有被拦截的评论内容。

## 如何构建自己的测试

### 1. 采集 Payload

从 Bitbucket 或生产环境日志中截取一个完整的 Webhook Payload 示例。它可以是 `pr:opened`, `pr:from_ref_updated` 或任何支持的事件。

### 2. 添加到 `requests/`

将 Payload 保存为 `.json` 文件放入 `test/e2e/requests/`。

> [!TIP]
> 使用前缀数字（如 `03_feature_x.json`）来控制测试执行的先后顺序。由于 PR 处理是基于 key-lock 的，这些请求将按顺序串行处理。

### 3. 配置环境与参数

在 `test/e2e/` 目录下创建以下本地配置文件（这些文件已被 Git 忽略）：

- **.env**: 参考根目录 `.env.example`（必须包含 `LLM_API_KEY`）。
- **config.test.yaml**: 参考根目录 `config.example.yaml`。建议将 `server.concurrency_limit` 设为 `1` 以确保串行执行。

```bash
# 设置示例
cp ../../.env.example .env
cp ../../config.example.yaml config.test.yaml
```

### 4. 运行测试

使用 `e2e` 标签运行测试。推荐加上 `-count=1` 以禁用缓存，强制执行 LLM 请求：

```bash
go test -v -count=1 -tags=e2e ./test/e2e
```

## 注意事项

- **超时控制**：测试为真实的 LLM 调用预留了 10 分钟的超时时间。如果 PR 非常大或网络缓慢，请耐心等待。
- **只读保证**：只要 `InterceptingTransport` 的逻辑未被修改，测试就不会向外部系统写入任何数据。
- **依赖说明**：运行测试需要本地或远程有可用的 MCP Server 且配置正确。
