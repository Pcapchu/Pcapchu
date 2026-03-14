# Pcapchu

Pcapchu 是一个面向 PCAP 的自动化网络流量取证系统，核心目标是让 Agent 在复杂、多轮调查中保持稳定推理和可控输出。

## 核心优势

- 多轮 `Plan-Execute-Summarize`：每轮先规划、再按步骤执行、最后汇总，支持持续下钻。
- `State Memo` 共享状态：执行链路仅依赖全局发现与操作日志推进，减少重复查库与无效工具调用。
- 分层上下文压缩：对话级压缩早期工具调用，轮次级压缩历史报告，并结合 SQLite 快照做跨轮记忆。
- SQL-first 分析：将 `conn/http/dns/ssl/files` 等异构日志统一到 DuckDB，支持跨表 JOIN 与多维聚合。
- 流级精确回溯：通过 `pkt2flow + flow_index` 先宏观定位可疑流，再做小切片包级解析，避免全量解包污染上下文。

## 系统组成

- Planner：基于查询与历史上下文生成调查计划。
- Executor：按计划步骤执行（ReAct），写入共享状态。
- Final Executor：仅基于共享状态生成本轮总结报告。
- Sandbox：Docker 隔离环境（Zeek、DuckDB、tshark、Scapy、pcapchu-scripts）。
- Storage：SQLite 持久化会话、轮次结果、事件、压缩快照。

## 运行依赖

- Go `1.25+`
- Docker Engine（Docker daemon 必须已启动）
- Sandbox 镜像：`pcapchu/sandbox:v1.0`
- 可用的 OpenAI 兼容模型服务（API Key + 模型名）
- （可选）Node.js + pnpm：仅在需要构建前端时使用

## 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `OPENAI_API_KEY` | 是 | LLM 服务 API Key |
| `OPENAI_MODEL_NAME` | 是 | 模型名（如 `gpt-4o`） |
| `OPENAI_BASE_URL` | 否 | OpenAI 兼容服务地址 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 否 | OTel 上报地址 |
| `OTEL_EXPORTER_OTLP_HEADERS` | 否 | OTel 认证头 |
| `OTEL_EXPORTER_OTLP_TIMEOUT` | 否 | OTel 超时（ms） |
| `OTEL_EXPORTER_OTLP_INSECURE` | 否 | OTel 是否使用非 TLS |

## 构建

### 仅构建 CLI（推荐）

```bash
go build -o pcapchu ./cmd/main.go
```

### 构建前端 + CLI

```bash
make build
```

> `make build` 需要本机安装 `pnpm`，且默认产物名是 `Pcapchu`。如果只用 CLI，可直接执行上面的 `go build -o pcapchu ...`。

## 使用方式

### 1) 新建分析会话

```bash
./pcapchu analyze --pcap ./capture.pcap --query "Find security threats" --rounds 2
```

常用参数：

- `--pcap`：新会话必填，PCAP 文件路径
- `--query`：调查问题（默认提供通用安全分析语句）
- `--rounds`：执行轮数，默认 `1`
- `--session`：指定会话 ID 时转为续跑模式
- `--db`：SQLite 路径，默认 `./pcapchu.db`

### 2) 续跑与会话管理

```bash
./pcapchu session list
./pcapchu session resume <session-id> --rounds 1 --query "Focus on lateral movement"
./pcapchu session delete <session-id>

./pcapchu pcap list
./pcapchu pcap delete <pcap-id>
```

### 3) 启动 HTTP/SSE 服务

```bash
./pcapchu serve --addr :8080
```

## 运行前检查

```bash
# 1) Docker daemon 是否可用
docker info > /dev/null

# 2) 建议提前拉取 sandbox 镜像
docker pull pcapchu/sandbox:v1.0

# 3) 设置必需环境变量
export OPENAI_API_KEY="<your-key>"
export OPENAI_MODEL_NAME="gpt-4o"
```

> 如果本地没有 sandbox 镜像，程序会在运行时尝试交互式拉取。
