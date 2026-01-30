> [Read in English](README.md)

# PR Review Automation with ADK-Go

## 项目概述

这是一个 **PR 自动审核工具**（`pr-review-automation`），基于 **Google ADK-Go v0.3.0** 框架构建，利用 **Gemini LLM** 智能代理自动审核 Bitbucket 的 Pull Request，并能结合 Jira 问题跟踪和 Confluence 文档进行综合评估。

---

## 技术栈

| 技术              | 版本/说明                                          |
| ----------------- | -------------------------------------------------- |
| **Go**            | 1.25.5                                             |
| **Google ADK-Go** | v0.3.0                                             |
| **Google GenAI**  | v1.42.0                                            |
| **MCP Client**    | github.com/modelcontextprotocol/go-sdk             |
| **LLM 模型**      | Gemini 1.5 Flash                                   |
| **监控**          | Prometheus Metrics                                 |
| **协议支持**      | MCP (Model Context Protocol)、A2A (Agent-to-Agent) |

---

## 项目结构

```
agent-sets/
├── cmd/
│   └── server/
│       └── main.go              # 服务入口点
├── internal/
│   ├── agent/
│   │   └── pr_review_agent.go   # ADK-Go PR 审查代理核心
│   ├── client/
│   │   ├── mcp.go               # MCP 客户端 (Bitbucket/Jira/Confluence)
│   │   └── mcp_test.go          # MCP 客户端测试
│   ├── config/
│   │   ├── config.go            # 环境变量配置加载
│   │   └── config_test.go       # 配置测试
│   ├── processor/
│   │   └── pr_processor.go      # PR 处理业务逻辑 + 评论回写
│   └── webhook/
│       ├── bitbucket.go         # Bitbucket Webhook 处理器
│       └── bitbucket_test.go    # Webhook 测试
├── go.mod
├── go.sum
└── README.md
```

---

## 架构图

```mermaid
flowchart TB
    subgraph 外部系统
        BB[Bitbucket DC]
        JIRA[Jira]
        CONF[Confluence]
    end

    subgraph MCP服务层
        BB_MCP[Bitbucket MCP Server]
        JIRA_MCP[Jira MCP Server]
        CONF_MCP[Confluence MCP Server]
    end

    subgraph agent-sets 服务
        WH[Webhook Handler]
        PROC[PR Processor]
        AGENT[PR Review Agent]

        subgraph 客户端层
            MCP_C[MCP Client]
        end

        subgraph ADK-Go 框架
            LLM[Gemini LLM Model]
            SESS[Session Service]
            RUNNER[Runner]
        end
    end

    BB -- Webhook 事件 --> WH
    WH -- 异步处理 --> PROC
    PROC --> AGENT
    AGENT --> LLM
    AGENT -- 动态工具调用 --> MCP_C
    AGENT --> RUNNER
    RUNNER --> SESS

    MCP_C -- MCP协议 --> BB_MCP
    MCP_C -- MCP协议 --> JIRA_MCP
    MCP_C -- MCP协议 --> CONF_MCP

    BB_MCP --> BB
    JIRA_MCP --> JIRA
    CONF_MCP --> CONF

    PROC -- 拉取历史 / 回写评论 --> MCP_C
```

---

## 工作流程 (Workflow)

以下时序图展示了从 Webhook 触发到评论回写的完整工作流与数据流转：

```mermaid
sequenceDiagram
    participant User as Developer
    participant BB as Bitbucket
    participant WH as Webhook Handler
    participant PROC as PR Processor
    participant AGENT as Review Agent
    participant LLM as Google Gemini
    participant MCP as MCP Client

    %% Data Flow: Raw JSON Payload
    User->>BB: Create/Update PR
    BB->>WH: POST /webhook (JSON Payload)
    WH-->>BB: 200 OK (Accepted)

    note right of WH: Transform: JSON -> Domain Object
    WH->>PROC: Enqueue Task (PR ID)

    %% Data Flow: Deduplication
    PROC->>MCP: 获取已有评论 (去重)
    MCP-->>BB: API 调用
    BB-->>MCP: 返回评论列表
    MCP-->>PROC: 现有 AI 评论

    PROC->>AGENT: Review(PR + 历史上下文)

    %% Data Flow: Context & Prompts
    loop Intelligence Loop
        AGENT->>LLM: Analyze (Prompt + Code Context)
        LLM-->>AGENT: Decision (Tool Call)
        AGENT->>MCP: Call Tool (Args)
        MCP-->>BB: Fetch Data (API)
        BB-->>MCP: Return Data (JSON)
        MCP-->>AGENT: Tool Output (Standardized)
    end

    %% Data Flow: Structured Review
    AGENT->>LLM: Final Decision
    LLM-->>AGENT: Review Comments (Structured)
    AGENT->>PROC: Return ReviewResult
    PROC->>MCP: Add Comments (API Call)
    MCP->>BB: Comments posted
```

---

## 核心模块详解

### 1. 服务入口 (`cmd/server/main.go`)

- 使用 `slog` 结构化日志
- 加载环境变量配置
- 初始化 **MCP 客户端**（统一管理 Bitbucket/Jira/Confluence 连接）
- 创建 PR 处理器和 Webhook 处理器
- 启动 HTTP 服务，支持优雅关闭

---

### 2. 配置模块 (`internal/config/config.go`)

工具支持通过 `config.yaml`（推荐）或环境变量进行配置。

#### 配置文件 (`config.yaml`)

每个 MCP 服务器都可以配置可选的 `allowed_tools` 白名单。如果省略或为空，则暴露所有工具。

```yaml
mcp:
  bitbucket:
    endpoint: "https://ai.mapscape.cn/bitbucket"
    allowed_tools:
      - bitbucket_get_pull_request
      - bitbucket_add_pull_request_comment
```

#### 环境变量

| 配置项        | YAML 路径               | 环境变量          | 说明                    |
| ------------- | ----------------------- | ----------------- | ----------------------- |
| LLM API Key   | `llm.api_key`           | `LLM_API_KEY`     | Gemini API 密钥         |
| 服务端口      | `server.port`           | `PORT`            | 默认 8080               |
| Bitbucket MCP | `mcp.bitbucket.*`       | `BITBUCKET_MCP_*` | Bitbucket MCP 服务/令牌 |
| Webhook 签名  | `server.webhook_secret` | `WEBHOOK_SECRET`  | HMAC 签名密钥           |
| Prompts 路径  | `prompts.dir`           | -                 | 提示词模板根目录        |

---

### 3. MCP 客户端 (`internal/client/mcp.go`)

基于 `github.com/modelcontextprotocol/go-sdk` 实现：

- **工具过滤**：支持 ADK-Go 原生的 `tool.StringPredicate`，可配置工具白名单。
- **熔断机制**：对故障的 MCP 服务器进行自动熔断保护。
- **动态适配**：将 MCP 工具动态适配为 ADK `tool.Tool` 接口。
- **直接访问**：提供 `CallTool` 方法供自动化回写任务使用。

---

### 4. PR 审查代理 (`internal/agent/pr_review_agent.go`)

**核心 AI 组件**，基于 ADK-Go 框架：

- **动态工具集**：使用 `mcpClient.GetAllTools` 加载 MCP 工具
- **增强提示词**：引导 Agent 主动调用工具获取代码变更和 Jira 信息
- **智能审查**：Agent 决策何时查询代码、Jira 或 Confluence

#### 审查输出格式：

```json
{
  "comments": [
    {
      "file": "filename.ext",
      "line": 123,
      "comment": "详细反馈"
    }
  ],
  "score": 85,
  "summary": "整体评估摘要"
}
```

---

### 5. PR 处理器 (`internal/processor/pr_processor.go`)

业务编排层：

- 接收 Webhook → 调用 Agent → 获取结果 → **回写评论**
- 解析 Agent 返回的结构化评论，调用 `bitbucket_add_pull_request_comment` 发布到 Bitbucket
- 支持文件级和行级评论

#### 评论展示样式 (Comment Output)

工具会在 Bitbucket PR 中自动发布两种类型的评论：

1.  **全局汇总 (Summary Comment)**:

    ```markdown
    **AI Review Summary (Model: Gemini 1.5 Flash)**
    Score: 85

    ## 主要发现

    - 代码整体结构良好，但在异常处理方面有待改进。
    - ...
    ```

2.  **代码行内评论 (Inline Comments)**:
    针对具体代码行的详细改进建议或 Bug 警告。

---

### 6. Webhook 处理器 (`internal/webhook/bitbucket.go`)

处理来自 Bitbucket Data Center 的 Webhook 事件：

- **支持事件类型**：`pr:opened`、`pr:updated`
- **安全特性**：
  - 使用 `http.MaxBytesReader` 限制请求体大小
  - HMAC-SHA256 签名验证（可选，通过 `WEBHOOK_SECRET` 启用）
- **并发控制**：使用信号量限制并发处理数量
- **异步处理**：接收请求后立即返回，后台处理 PR

---

## 主要功能

1. ✅ **自动化 PR 审查**：接收 Bitbucket Webhook，使用 AI 自动分析代码变更
2. ✅ **MCP 工具集成**：动态加载 Bitbucket/Jira/Confluence 工具
3. ✅ **评论回写**：将审查结果自动发布到 Bitbucket PR
4. ✅ **代码质量分析**：检测潜在 Bug、安全问题、性能问题
5. ✅ **Jira 对齐验证**：检查 PR 是否与相关 Jira 问题对齐
6. ✅ **并发安全**：限制并发处理数量，防止资源耗尽
7. ✅ **优雅关闭**：支持信号触发的优雅关闭
8. ✅ **持久化存储**：保存审查历史和指标到 SQLite
9. ✅ **智能去重**：Bitbucket 原生评论去重，防止重复评论
10. ✅ **智能去重**：Bitbucket 原生评论去重，防止重复评论
11. ✅ **混合评论模式 (Hybrid Mode)**：重要的 Bug (CRITICAL/WARNING) 作为行内注释发布（便于精准定位），而琐碎的建议 (INFO/NIT) 则汇总到总结报告中（减少视觉干扰）。
12. ✅ **高可靠性**：全面的超时保护（关机、LLM、MCP）、并发控制及外部依赖熔断机制。

---

## 待完善功能

| 功能         | 状态      | 说明                       |
| ------------ | --------- | -------------------------- |
| 持久化存储   | ✅ 已完成 | 审查历史和指标存储         |
| PostgreSQL   | 未实现    | 生产级数据库支持           |
| 历史记录 API | 未实现    | 查询审查记录的 HTTP 接口   |
| 管理后台     | 未实现    | 审查数据与指标的可视化看板 |
| 更多审查算法 | 未实现    | 可扩展的审查规则           |

---

## 部署说明

详细的生产环境部署说明（包括 Webhook 配置、环境变量详解及安全建议），请参阅 [部署指南](docs/deployment.zh.md)。

## 快速开始

1. 安装依赖：

   ```bash
   go mod tidy
   ```

2. 配置文件：

   参考 `config.example.yaml` 创建 `config.yaml`：

   ```yaml
   llm:
     api_key: "your_gemini_api_key"
   mcp:
     bitbucket:
       endpoint: "http://bitbucket-mcp:8080"
       token: "optional_token"
   ```

3. 运行服务：

   ```bash
   go run cmd/server/main.go
   ```

4. 运行测试：
   ```bash
   go test ./... -v
   ```

### Docker 部署

1. 构建镜像：

   ```bash
   docker build -t pr-review-automation:latest .
   ```

2. 运行容器：

   ```bash
   docker run -d \
     -p 8080:8080 \
     -e LLM_API_KEY="your_gemini_api_key" \
     -e BITBUCKET_MCP_ENDPOINT="http://bitbucket-mcp:8080" \
     -e BITBUCKET_MCP_TOKEN="your_token" \
     -e WEBHOOK_SECRET="your_webhook_secret" \
      -e WEBHOOK_SECRET="your_webhook_secret" \
      -v $(pwd)/data:/app/data \
      --name pr-review \
      pr-review-automation:latest
   ```

3. 查看日志：
   ```bash
   docker logs -f pr-review
   ```

---

## Prompt 的重要性与设计规范

在基于 LLM 的智能代理系统中，Prompt 的设计直接决定了系统的稳定性、效率和反馈质量。

### 1. 为什么 Prompt 至关重要？

- **防止逻辑死循环**：如果 Prompt 没有明确规定 Agent 在何种条件下应该停止尝试或上报错误，Agent 可能会在无法获取正确信息时陷入“获取不到数据 -> 再次调用工具 -> 依然获取不到”的死循环，导致资源浪费和响应超时。
- **避免思考类 LLM 陷入无限思考**：对于具备强推理能力的“思考型”模型（如 o1, Gemini Thinking 等），不明确或矛盾的指令可能导致模型在思维链（Chain of Thought）中反复推演而无法得出结论，甚至陷入逻辑死胡同，造成巨大的推理开销和延迟。
- **确保解析成功**：本系统依赖结构化的 JSON 输出与 Bitbucket 进行交互。如果 Prompt 未能严格约束输出格式（例如要求纯 JSON 而非 Markdown 包装），或者未定义清晰的字段类型，可能导致后端解析失败，甚至发布错误的评论信息。
- **提高审查精度**：模糊的指令会导致 LLM 产生幻觉（Hallucination），误报不存在的代码问题。明确的审查标准和上下文引导能够显著降低噪声。

### 2. 设计规范建议

- **明确输出模式**：始终要求 LLM 以特定格式（如 JSON）返回，并提供示例模版。
- **边界条件处理**：在 Prompt 中明确指出当工具调用返回空数据或报错时，Agent 应该采取的降级策略（如忽略该文件或在总结中注明）。
- **职责范围限定**：限制 Agent 的动作范围，防止其执行非预期的工具调用。

---

## 本地 LLM 配置推荐

本系统支持通过 OpenAI 兼容接口连接本地 LLM（如 Ollama, vLLM）。为了获得最佳的代码审查效果，请参考以下配置建议：

### 1. 模型选择 (Model Selection)

- **推荐模型**：强烈建议使用经过代码微调且参数量适中的模型，如：
  - **Qwen 3 Coder** (30B)
  - **GLM 4.7** (23B / 30B)
- **避免使用**：通用的聊天模型可能在代码理解和 JSON 格式遵循上表现不佳。

### 2. 参数配置 (Configuration)

在 `config.yaml` 中进行如下调整：

- **`llm.timeout`**: **120s+**。代码审查涉及大量 Context 处理，本地推理速度较慢，请务必增加超时时间。
- **`pipeline.stage3_review.max_context_tokens`**: **32k+**。取决于您的显存大小。较大的上下文窗口允许 Agent 一次性读取更多代码文件，减少分片处理的由于。
- **`pipeline.stage3_review.temperature`**: **0.1 - 0.3**。低温度有助于生成更稳定、确定性的 JSON 输出。

### 3. 推荐推理服务配置 (llama.cpp)

为了在消费级硬件（如双卡 RTX 2080Ti）上获得最佳的长上下文（100k+ tokens）性能，建议使用以下 `llama-server` 启动配置：

```bash
llama-server \
  --model path/to/model.gguf \
  --port 8080 \
  --split-mode layer \
  --flash-attn on             # 【必须】开启 Flash Attention 以降低显存占用并提升速度
  --ctx-size 102400           # 100k 上下文窗口
  --batch-size 512            # 吞吐量平衡点
  --ubatch-size 256           # 限制物理批次大小，防止显存尖峰
  --cache-type-k q4_0         # K 缓存 4-bit 量化
  --cache-type-v q4_0         # V 缓存 4-bit 量化
  --no-mmap                   # 强制加载到显存
  --mlock                     # 锁定内存，防止交换
  --cache-reuse 256           # 允许复用缓存，加速 Prompt 处理
  --temp 0.1                  # 低温度，保证输出确定性
  --seed 42                   # 固定种子，便于复现结果
```

---

de

## 扩展系统

模块化架构使系统易于扩展：

1. **添加 MCP 服务**：在 `config.go` 中添加新的 Endpoint/Token 配置
2. **自定义代理行为**：修改 `pr_review_agent.go` 中的指令提示词
3. **增强审查流程**：在 `pr_processor.go` 中添加新的处理阶段

---

## 总结

- ✅ 模块化清晰的项目结构
- ✅ 基于 ADK-Go 的智能代理架构
- ✅ MCP 工具动态集成
- ✅ 审查结果自动回写
- ✅ 并发安全的 Webhook 处理
- ✅ 优雅关闭机制
- ✅ 结构化日志
- ✅ 单元测试覆盖
