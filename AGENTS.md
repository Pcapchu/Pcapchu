# AGENTS.md — Pcapchu Development Reference

This file provides detailed context for coding agents working on the Pcapchu codebase.
For a human-friendly overview, see [README.md](./README.md).

---

## Table of Contents

- [Project Overview](#project-overview)
- [Architecture](#architecture)
  - [System Architecture](#system-architecture)
  - [ReAct Agent Loop (Eino)](#react-agent-loop-eino)
  - [Multi-Agent Pipeline](#multi-agent-pipeline)
  - [Event System](#event-system)
  - [HTTP SSE Server](#http-sse-server)
- [Project Structure](#project-structure)
- [Build & Run](#build--run)
- [Environment Variables](#environment-variables)
- [Key Dependencies](#key-dependencies)
- [Data Layer: pcapchu-scripts](#data-layer-pcapchu-scripts)
- [Storage Schema](#storage-schema)
- [Coding Conventions](#coding-conventions)
- [Testing](#testing)

---

## Project Overview

Pcapchu is an autonomous AI agent that investigates network packet captures using a **Multi-Agent Plan-Execute-Summarize architecture**.
Given a pcap file and a natural-language query, it:

1. Spins up an isolated Docker sandbox with Zeek, DuckDB, tshark, and Scapy.
2. Runs a **Planner Agent** that inspects metadata and produces an investigation plan (JSON).
3. Runs **Executor Agents** — one per plan step — that execute commands, collect findings, and build a research report.
4. A **Round Summary Agent** (final executor) synthesizes all findings into a structured report.
5. Persists session state (rounds, findings, pcap references) to SQLite for resume.

Multiple investigation rounds can be executed, each building on the previous round's context.

### System Architecture Highlights

**Problem Statement:**  
Traditional network forensics tools suffer from limited cross-protocol semantic reasoning and low-signal-to-noise ratio in logs, which can easily overwhelm LLM context windows during complex investigations.

**Solution Architecture:**

- **Three-Stage Pipeline**: Planner (strategic planning) → Executor (tactical execution) → Summarizer (synthesis) per round.
  
- **Shared State Model (State Memo)**: PlanState flows through executors, accumulating key findings and operation logs. Normal executors extract core findings after each step; subsequent executors and Final Executor depend solely on this global state to prevent redundant tool calls and dynamic loops.

- **Hierarchical Context Compression**:
  - *Conversation-level*: Compresses early tool-calling history into summaries while preserving recent interactions (~20K tokens).
  - *Round-level*: Compresses historical reports and loads them on-demand via `history_snapshots` table.
  - *Cross-round memory*: SQLite persistence enables long-term context across investigation rounds.

- **High-Semantic-Density SQL Interface**: Heterogeneous protocol logs (`conn`, `http`, `dns`, `ssl`, `files`) unified into columnar SQL via DuckDB, supporting cross-table JOINs and multi-dimensional aggregations in a single query, replacing low-efficiency plain-text scanning with precision relational extraction.

- **Five-Tuple Flow Slicing + `flow_index` Index**: `pkt2flow` splits PCAP by 5-tuple; agents first locate suspicious flows via macro-level SQL, then precisely map to per-flow PCAP slices for packet-level (`tshark`) analysis, eliminating context pollution from full-PCAP packet dumps.

---

## Architecture

### System Architecture

```
+--------+     +------------------+     +-------------------------+
|  User  |---->|  CLI (cobra)     |---->|  Runtime Bootstrap      |
+--------+     |  cmd/main.go     |     |  internal/cli/          |
   |           |  internal/cli/   |     |  - LLM client (OpenAI)  |
   |           +------------------+     |  - Docker sandbox       |
   |                                    |  - Logger + OTel        |
   |                                    |  - Event emitter        |
   |                                    +-----------+-------------+
   |                                                |
   |  +------------------+                          v
   +->|  HTTP Server     |     +-----------------+------------------+
      |  internal/server/ |     |        Investigation Loop          |
      |  - SSE streaming  |---->|  internal/investigation/           |
      |  - REST API       |     |  (per round, per session)          |
      +------------------+
                                  |                                    |
                                  |  +----------+     +------------+  |
                                  |  | Planner  |---->| Executor   |  |
                                  |  | Agent    |     | Agent(s)   |  |
                                  |  +----------+     +------+-----+  |
                                  |       |                  |         |
                                  |       v                  v         |
                                  |  +----------------------------+   |
                                  |  |     Docker Sandbox          |   |
                                  |  |  +-----------------------+  |   |
                                  |  |  | pcapchu-scripts       |  |   |
                                  |  |  | (Zeek + DuckDB)       |  |   |
                                  |  |  +-----------------------+  |   |
                                  |  |  | bash / tshark / scapy |  |   |
                                  |  |  +-----------------------+  |   |
                                  |  +----------------------------+   |
                                  +-----------------+------------------+
                                                    |
                                                    v
                                  +-----------------+------------------+
                                  |           Persistence              |
                                  |  +----------+  +--------------+   |
                                  |  | SQLite   |  | Event Bus    |   |
                                  |  | (sqlx)   |  | (channels)   |   |
                                  |  +----------+  +--------------+   |
                                  +------------------------------------+
```

### ReAct Agent Loop (Eino)

Each agent (Planner and Executor) uses the Eino framework's ReAct pattern with stateful message rewriting.
The loop follows this flow for each agent invocation:

```
1. Input Messages
        |
        v
+-------+--------+
| StatePreHandler |  2-1. Add input/tool messages to state
|   (prepare)     |  2-2. Use state's message list as ChatModel input
+---------+-------+  2-3. Decorate message list by user's Modifier
          |
          v
  +-------+-------+
  | MessageRewriter|  2-4. (Executor only) Compress old messages
  | (Summarizer)  |       while keeping recent ~20K tokens
  +-------+-------+
          |
          v
  +-------+-------+
  |   ChatModel   |  3. LLM generates a response
  | (OpenAI API)  |
  +-------+-------+
          |
          v  ChatResponse
    +-----+------+
    | tool call? |  4. Check first frame for tool calls
    +-----+------+
      |         |
      | N       | Y
      v         v
  +---+---+ +---+------------+
  |  End  | | StatePreHandler|  5. Add tool call message to state
  +---+---+ +--------+-------+
      |               |
      v               v
  Final         +-----+------+
  Message       |  ToolsNode |  6. Execute tool (bash, file editor),
                |            |     get Tool Response
                +-----+------+
                      |
                      v
                Go back to StatePreHandler (step 2-1)
```

**Key configuration:**

- **Planner Agent**: `maxStep = 200` (graph node limit), lightweight metadata queries + lightweight SQL.
- **Executor Agent**: `maxStep = 200`, deep analysis with heavy tool usage.
  - Rewriter pipeline: `LangHintModifier` (detects query language) → `ConversationSummarizer` (compresses messages).
  - Supports up to ~100 ReAct loops per executor (5 graph nodes × 20 max steps).
- **Message Rewriter** (Executor only): Intelligently compresses tool-calling history using `sum.md` prompt while preserving recent messages to manage context window explosions during long investigations.

**State Persistence During Execution:**
- `PlanState` is created fresh per `Executor.Run()` invocation.
- State flows through graph via `compose.WithGenLocalState()` and pre/post hooks.
- Findings and operation logs accumulate in-memory; flushed to SQLite after round completes.

### Multi-Agent Pipeline

Each investigation round runs the following pipeline with **shared state propagation**:

```
                     Round N
                        |
                        v
+----------+    +--------------+    +------------------+
| Load     |--->| Planner      |--->| Plan (JSON)      |
| History  |    | Agent        |    | {thought, steps,  |
| (SQLite) |    | (maxStep=15) |    |  table_schema}   |
+----------+    +--------------+    +--------+---------+
                                             |
           +---------------------------------+
           |
           v
   +-------+--------+
   | PlanState Init  |  (shared state carries forward)
   | ResearchFindings=""
   | OperationLog=[]
   | KeyFindingsHistory (if multi-round)
   +-------+--------+
           |
           v
   +-------+--------+
   | For each step:  |
   |                 |
   |  step 1..N-1:  |     +-----------+     +---------------------+
   |  Normal Step   |---->| Executor  |---->| Append to          |
   |                |     | Agent     |     | ResearchFindings   |
   |                |     | (ReAct)   |     | + OperationLog     |
   |                |     +-----------+     +---------------------+
   |                |
   |  step N:       |     +-----------+     +---------------------+
   |  Final Step    |---->| Summarizer|---->| {summary,          |
   |                |     | (ChatModel|     |  key_findings,     |
   |                |     |  only)    |     |  open_questions,   |
   |                |     +-----------+     |  markdown_report}  |
   +-------+--------+                       +---------------------+
           |                                        |
           v                                        v
   +-------+----------+                   +--------+----------+
   | Context          |                   | Round Result      |
   | Compression      |                   | (in-memory buffer)|
   | (if needed)      |                   +--------+----------+
   +-------+----------+                           |
           |                                      v
           +----> SQLite Persist (Round+Events+Snapshots)
                  (Atomic flush on SSE close OR CLI completion)
```

**Key Points:**

1. **State Memo Flow**: `PlanState` encapsulates `ResearchFindings`, `OperationLog`, `CurrentStepIndex`. Each normal executor step appends to global findings and logs; Final Executor reads accumulated state (no re-execution).

2. **Compression Checkpoint**: Before each round, key findings and planner history are loaded from snapshots (`history_snapshots` table) or raw rounds. If compressed, early rounds are replaced by LLM summaries, preserving recent raw context.

3. **Atomic Persistence**: 
   - CLI: `defer cleanup()` flushes to SQLite after `RunInvestigation` completes.
   - Server: `roundCollector` buffers events + round data in memory during SSE stream; on success, flushed atomically; on client disconnect, discarded (no partial DB writes).

**Prompts:**
- Planner prompt: `internal/prompts/planner.md`
- Normal Executor prompt: `internal/prompts/normal_executor.md`
- Final Executor prompt: `internal/prompts/final_executor.md`
- Conversation Summarizer: `internal/prompts/sum.md`
- Report Summarizer: `internal/prompts/sum_report.md`

### Event System

The event bus uses Go channels with configurable buffer size (default: 1024).
`Emit()` is blocking (no silent drops). Events are typed:

```
// Session lifecycle
session.created     session.resumed

// Analysis lifecycle
analysis.started    analysis.completed

// Pcap
pcap.loaded

// Round lifecycle
round.started       round.completed

// Planner
plan.created        plan.error

// Executor
step.started        step.findings
step.completed      step.error

// Final
report.generated

// General
info                error
```

Subscribers receive `Event{Type, SessionID, Timestamp, Data}` via channel.

In **server mode**, events are also persisted to `session_events` table with
monotonic sequence numbers for SSE replay and reconnection support.

Event data payloads (decoded from `Data json.RawMessage` based on `Type`):

| Event Type | Data Struct | Key Fields |
|---|---|---|
| `session.created` | `SessionCreatedData` | SessionID, UserQuery, PcapSource |
| `session.resumed` | `SessionResumedData` | SessionID, FromRound |
| `analysis.started` | `AnalysisData` | SessionID, TotalRounds |
| `analysis.completed` | `AnalysisData` | SessionID, TotalRounds |
| `pcap.loaded` | `PcapLoadedData` | Source, Path, Size, Filename |
| `round.started` | `RoundStartedData` | Round, TotalRounds |
| `round.completed` | `RoundCompletedData` | Round, Summary, KeyFindings, MarkdownReport |
| `plan.created` | `PlanCreatedData` | Thought, TotalSteps, Steps |
| `plan.error` | `ErrorData` | Phase, Message, StepID |
| `step.started` | `StepStartedData` | StepID, Intent, TotalSteps |
| `step.findings` | `StepFindingsData` | StepID, Intent, Findings, Actions |
| `step.completed` | `StepCompletedData` | StepID, TotalSteps |
| `step.error` | `ErrorData` | Phase, Message, StepID |
| `report.generated` | `ReportData` | Round, Report, MarkdownReport, ContentLen, TotalSteps, DurationMs |
| `info` | `InfoData` | Message |
| `error` | `ErrorData` | Phase, Message, StepID |

### HTTP SSE Server

The HTTP server (`internal/server/`) provides a REST + SSE API for web frontends.
Full API documentation: [`internal/server/API.md`](./internal/server/API.md).

```
   Frontend (browser)
       │
       ├── POST /api/pcap/upload              → JSON: upload pcap + create session
       ├── POST /api/sessions/{id}/analyze    → SSE: start/continue investigation
       ├── GET  /api/sessions/{id}/events     → JSON: stored event history
       ├── GET  /api/sessions                 → JSON: list sessions
       └── PATCH /api/sessions/{id}/pcap      → JSON: re-attach pcap to session
              │
              ▼
   ┌──────────────────────────────────────────┐
   │  Server (internal/server/)               │
   │                                          │
   │  ┌─────────┐    ┌────────────────────┐   │
   │  │ Router  │───>│ Per-request Runtime │   │
   │  │ (mux)   │    │ (investigation.     │   │
   │  └─────────┘    │  NewRuntime)        │   │
   │                 │ - Docker sandbox    │   │
   │                 │ - LLM + agents     │   │
   │                 │ - Event emitter    │   │
   │                 └────────┬───────────┘   │
   │                          │               │
   │                          ▼               │
   │            investigation.RunInvestigation│
   │                          │               │
   │                          ▼               │
   │              SSE client ← event stream   │
   │              session_events ← persist    │
   └──────────────────────────────────────────┘
```

Key design:
- **Upload creates session** — `POST /api/pcap/upload` stores the pcap and
  creates a session bound to it, returning the `session_id`.
- **One Runtime per request** — `POST /api/sessions/{id}/analyze` creates
  an `investigation.Runtime` (same as CLI), runs the investigation
  synchronously within the SSE response, then tears down. No global state.
- **Unified endpoint** — the analyze endpoint handles both first analysis
  and continuation by auto-detecting via round count in the DB.
- **SSE = inline with analysis** — the analysis endpoint IS the SSE stream.
  Events are streamed live as the investigation runs.
- **Client disconnect = cancel** — closing the HTTP connection cancels
  `r.Context()`, which propagates to the runtime and triggers cleanup.
- **Cleanup guaranteed** — `rt.Close()` runs via `defer` (Docker sandbox
  cleanup + emitter close).
- **History via JSON** — `GET /sessions/{id}/events` returns all persisted
  events for past sessions. SSE is for live streaming only.

---

## Project Structure

```
Pcapchu/
|-- cmd/
|   `-- main.go                    # Entry point — calls cli.Execute()
|-- internal/
|   |-- cli/                       # CLI layer (cobra commands + runtime)
|   |   |-- root.go                #   Root command, Execute(), --db flag
|   |   |-- runtime.go             #   CLI runtime wrapper (signal handling, OTel, event printer)
|   |   |-- analyze.go             #   "analyze" subcommand, resumeSession()
|   |   |-- serve.go               #   "serve" subcommand (HTTP SSE server)
|   |   |-- session.go             #   "session list/resume/delete"
|   |   `-- pcap.go                #   "pcap list/delete"
|   |-- common/
|   |   |-- types.go               #   Plan, NormalOutput, RoundSummary, SessionHistory
|   |   `-- utils.go               #   Shared utilities
|   |-- events/
|   |   `-- events.go              #   ChannelEmitter, event types, subscriber channels
|   |-- executor/
|   |   `-- executor.go            #   Executor graph (normal + final step pipeline)
|   |-- investigation/
|   |   |-- investigation.go       #   RunInvestigation, CopyPcapToContainer, NewReActAgent
|   |   `-- runtime.go             #   Runtime: shared init (Docker, LLM, agents)
|   |-- planner/
|   |   `-- planner.go             #   Planner graph (prompt + ReAct + JSON parse)
|   |-- prompts/
|   |   |-- prompts.go             #   Prompt template loader (embed)
|   |   |-- planner.md             #   Planner system prompt
|   |   |-- normal_executor.md     #   Normal executor system prompt
|   |   |-- final_executor.md      #   Round summary agent prompt
|   |   |-- analyzer_introduction.md  # Shared sandbox context
|   |   |-- sum.md                 #   Conversation summarizer prompt
|   |   `-- sum_report.md          #   Report summarizer prompt (history compression)
|   |-- server/                    # HTTP SSE API server
|   |   |-- API.md                 #   Full API documentation
|   |   |-- server.go              #   Server struct, routes, CORS, ListenAndServe
|   |   |-- sse.go                 #   SSE writer helper (writeEvent, writeComment)
|   |   |-- handler_analyze.go     #   POST /api/sessions/{id}/analyze (SSE)
|   |   |-- handler_stream.go      #   GET /api/sessions/{id}/events (JSON history)
|   |   |-- handler_session.go     #   GET/DELETE /api/sessions
|   |   `-- handler_pcap.go        #   POST /api/pcap/upload (creates session), GET/DELETE, PATCH reattach
|   `-- storage/
|       |-- models.go              #   PcapFile, Session, Round, SessionEvent models
|       `-- store.go               #   SQLite CRUD (sqlx), schema DDL, migrations
|-- middlewares/
|   |-- logger/
|   |   |-- logger.go              #   Log interface, Logger struct, Emit(), NewDefaultLogger
|   |   |-- sink.go                #   Sink interface, NopSink, MultiSink
|   |   |-- console_sink.go        #   Console sink with content truncation
|   |   |-- pretty_handler.go      #   Custom slog handler (colored, multi-line)
|   |   |-- slog_sink.go           #   slog adapter helpers
|   |   |-- otel_sink.go           #   OpenTelemetry sink (logs + traces + metrics)
|   |   |-- otel_setup.go          #   OTel provider bootstrap (InitOTel)
|   |   `-- logger_callback.go     #   Eino callback handler (logs input/output)
|   |-- summarizer/
|   |   |-- compressor.go          #   HistoryCompressor: LLM-based round history compression
|   |   |-- config.go              #   Summarizer configuration
|   |   |-- define.go              #   Error definitions
|   |   `-- summary.go             #   Conversation & Report summarizers (context window mgmt)
|   `-- token_counter/
|       `-- token_counter.go       #   tiktoken-based token counting
|-- sandbox/
|   |-- Dockerfile                 #   Sandbox Docker image definition
|   |-- dockerfile_version.txt     #   Image tag version (e.g. "v1.0")
|   |-- image.go                   #   ImageName() — embeds version, returns repo:tag
|   |-- environment/
|   |   `-- docker.go              #   DockerEnv: container lifecycle, file copy
|   `-- tools/
|       |-- bash.go                #   BashTool (command execution in sandbox)
|       |-- output_guard.go        #   OutputGuard: truncates oversized tool output
|       |-- safe_sre.go            #   SafeStrReplaceEditor tool
|       `-- safe_wrapper.go        #   Tool safety wrapper (errors → string results)
`-- pcapchu-scripts/               # Python data layer (separate project, copied here)
    `-- src/pcapchu_scripts/
        |-- cli.py                 #   CLI: init, meta, query, ingest, serve
        |-- service.py             #   Facade: PcapchuScripts orchestrator
        |-- db.py                  #   DuckDB wrapper
        |-- zeek.py                #   Zeek runner
        |-- pkt2flow.py            #   pkt2flow + flow_index table
        |-- ingest.py              #   Log discovery + DuckDB ingestion
        |-- metadata.py            #   Schema catalogue (_meta_tables)
        |-- query.py               #   SQL execution with row limit
        |-- toon.py                #   Token-Oriented Object Notation encoder
        |-- types.py               #   Domain dataclasses
        `-- errors.py              #   Exception hierarchy
```

---

## Build & Run

### Build

```bash
go build -o pcapchu ./cmd/
```

### Lint / Vet

```bash
go vet ./...
```

### Run

```bash
# New analysis (CLI — pcap is always stored in the DB)
./pcapchu analyze --pcap capture.pcap --query "Find security threats" --rounds 2

# Resume a session
./pcapchu session resume <session-id> --rounds 1

# List sessions / pcap files
./pcapchu session list
./pcapchu pcap list

# Start HTTP SSE server
./pcapchu serve --addr :8080
```

### Docker Sandbox Image

The sandbox image is built from `sandbox/Dockerfile` and tagged via
`sandbox/dockerfile_version.txt` (currently `v1.0`).
`sandbox.ImageName()` returns `pcapchu/sandbox:<version>`.

It contains:
- Ubuntu 24.04, user `linuxbrew` with passwordless sudo
- Python 3.12 venv with scapy, pyshark, pandas, ipython, requests, pytz
- Zeek, tshark (via wireshark), pkt2flow (built from source)
- gron, jq, tree
- pcapchu-scripts (installed via uv/pip from GitHub)
- Homebrew package manager

---

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `OPENAI_API_KEY` | Yes | API key for LLM |
| `OPENAI_MODEL_NAME` | Yes | Model name (e.g. `gpt-4o`, `deepseek-chat`) |
| `OPENAI_BASE_URL` | No | Base URL for OpenAI-compatible API |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | No | Enables OTel export (e.g. `http://localhost:4317`) |
| `OTEL_EXPORTER_OTLP_HEADERS` | No | Auth headers for OTel endpoint |
| `OTEL_EXPORTER_OTLP_TIMEOUT` | No | gRPC timeout in ms |
| `OTEL_EXPORTER_OTLP_INSECURE` | No | `true` for HTTP endpoints |

---

## Key Dependencies

| Package | Version | Purpose |
|---|---|---|
| `github.com/cloudwego/eino` | v0.7.37 | AI agent framework (ReAct, graph, callbacks) |
| `github.com/cloudwego/eino-ext/components/model/openai` | v0.1.8 | OpenAI chat model |
| `github.com/cloudwego/eino-ext/components/tool/commandline` | v0.0.0-2026… | Sandbox command-line tool (bash, str_replace_editor) |
| `github.com/spf13/cobra` | v1.10.2 | CLI framework |
| `github.com/jmoiron/sqlx` | v1.4.0 | SQL helper layer |
| `modernc.org/sqlite` | v1.46.1 | Pure-Go SQLite driver |
| `github.com/google/uuid` | v1.6.0 | UUID generation (session IDs) |
| `go.opentelemetry.io/otel` | v1.42.0 | OpenTelemetry SDK |
| `github.com/docker/docker` | v28.0.4 | Docker API client |
| `github.com/pkoukk/tiktoken-go` | v0.1.8 | Token counting |
| `github.com/bytedance/sonic` | v1.15.0 | Fast JSON (indirect, Go 1.25 compat) |

---

## Data Layer: pcapchu-scripts

`pcapchu-scripts` is a Python CLI tool that provides the **structured, high-semantic-density data layer**
inside the Docker sandbox. It transforms raw PCAP files into SQL-queryable interfaces.

### Processing Pipeline

```
pcap file
     |
     v
  zeek -C -r capture.pcap          -- produces JSON log files (NDJSON format)
  (json-logs, extract-all-files)   -- policies for structured logging
     |
     v
  DuckDB ingestion                  -- auto-detect schema, load each .log → table
  (read_json_auto)                  -- union_by_name=true handles schema variance
     |
     v
  pkt2flow                          -- splits pcap into per-flow .pcap files
  (-u, -v, -x flags)                -- organized by protocol (tcp_syn, tcp_nosyn, udp, icmp)
     |
     v
  flow_index table                  -- SQL-queryable 5-tuple → file_path index
  (protocol, src_ip, src_port,      -- enables agent to drill from macro SQL
   dst_ip, dst_port, ts_epoch)      -- to packet-level pcap slices
     |
     v
  _meta_tables                      -- schema catalogue + row counts
                                    -- agents query this first to understand available data
```

### Commands (used by agents inside the sandbox)

```bash
cd /home/linuxbrew && pcapchu-scripts init <pcap>     # Full pipeline
cd /home/linuxbrew && pcapchu-scripts meta             # Print schema (TOON format)
cd /home/linuxbrew && pcapchu-scripts query "<SQL>"    # Execute DuckDB SQL
cd /home/linuxbrew && pcapchu-scripts ingest           # Ingest existing Zeek logs only
cd /home/linuxbrew && pcapchu-scripts serve            # Start stdin/stdout JSON-RPC server
```

**Global CLI options:** `-w/--work-dir` (working directory), `--db` (DuckDB path), `-v/--verbose`

**`init` flags:** `--no-zeek`, `--no-pkt2flow`, `--keep-logs`

**`query` flags:** `--limit` (max rows, default 50000)

**`meta` flags:** `--json` (output JSON instead of TOON)

**`serve` protocol:** Stdin/stdout JSON-RPC for AI agents:
```json
→ {"method": "query", "params": {"sql": "SELECT ...", "max_rows": 50000}}
← {"result": {...}}
```

### Key Tables

| Table | Description | Agent Query Pattern |
|---|---|---|
| `conn` | Connection records (5-tuple, duration, bytes, history flags) | Count by state, detect long-lived conns |
| `dns` | DNS queries (query, response, TTL, rejected) | Find suspicious domains, failed lookups |
| `http` | HTTP requests (host, URI, method, response code, fuids) | Detect C2 beacons, exfil patterns |
| `ssl` | TLS/SSL handshake info (server_name, ja3, cert hash, version) | Identify uncommon ciphers, cert anomalies |
| `files` | File analysis (MIME, size, hash, extracted path) | Cross-reference suspicious downloads, hashes |
| `flow_index` | 5-tuple → per-flow PCAP file path index | Map SQL results to packet-level slices |
| `_meta_tables` | Schema catalogue (name, row count, columns, types) | First query: agents discover available data |

### SQL Querying Patterns

**Agent use patterns (via `pcapchu-scripts query`):**

1. **Schema discovery** (on each round start):
   ```sql
   SELECT * FROM _meta_tables;  -- agent learns what tables exist and their sizes
   ```

2. **Cross-protocol anomaly detection**:
   ```sql
   SELECT c.src_ip, c.dst_ip, COUNT(c.uid) as conn_count,
          COUNT(DISTINCT h.host) as http_hosts,
          COUNT(DISTINCT d.query) as dns_queries
   FROM conn c
   LEFT JOIN http h ON (c.src_ip = h.src_ip AND c.src_port = h.src_port)
   LEFT JOIN dns d ON (c.src_ip = d.src_ip)
   WHERE c.duration > 3600
   GROUP BY c.src_ip, c.dst_ip
   ORDER BY conn_count DESC;
   ```

3. **Flow slice mapping** (from SQL to packet-level):
   ```sql
   SELECT file_path FROM flow_index
   WHERE src_ip = '192.168.1.10' AND dst_port = 443
   LIMIT 1;  -- returns e.g., /home/linuxbrew/output_flows/tcp_syn/192.168.1.10_443_8.8.8.8_443_1234567890.pcap
   ```

4. **Multi-protocol correlation**:
   ```sql
   SELECT DISTINCT c.uid, c.src_ip, c.dst_ip, c.dst_port,
          h.host, h.method, h.uri,
          s.server_name, s.version
   FROM conn c
   LEFT JOIN http h ON h.uid = c.uid
   LEFT JOIN ssl s ON s.uid = c.uid
   WHERE c.dst_port IN (80, 443, 8080);
   ```

---

## Storage Schema

SQLite database (`--db` flag, default `./pcapchu.db`):

```sql
-- Pcap binary blobs (optional, deduplicated by SHA-256)
pcap_files (id, filename, size, sha256 UNIQUE, data BLOB, created_at)

-- Investigation sessions
sessions (id TEXT PK, user_query, pcap_file_id FK -> pcap_files ON DELETE SET NULL,
          pcap_path, findings_summary, report_summary,
          status TEXT DEFAULT 'idle',
          created_at, updated_at)

-- Per-round investigation results
rounds (id, session_id FK -> sessions ON DELETE CASCADE, round,
        research_findings, operation_log, summary, key_findings,
        open_questions, markdown_report, compressed, created_at,
        UNIQUE(session_id, round))

-- Compressed history snapshots (one per session + scope)
history_snapshots (id, session_id FK -> sessions ON DELETE CASCADE, scope,
                   compressed_up_to, content, created_at,
                   UNIQUE(session_id, scope))

-- SSE event replay (monotonic seq per session)
session_events (id, session_id FK -> sessions ON DELETE CASCADE, seq,
                event_type, data TEXT, created_at,
                UNIQUE(session_id, seq))
```

---

## Coding Conventions

- **Go version**: 1.25+ (module: `github.com/Pcapchu/Pcapchu`)
- **Imports**: stdlib, then project packages, then third-party. Grouped with blank lines.
- **Error handling**: `fmt.Errorf("context: %w", err)` — always wrap with context.
- **Logging**: Use `logger.Log` interface. Never use `fmt.Println` for operational output (use structured logging or events).
- **Sinks**: Console sink truncates long strings (default 2000 chars). OTel sink gets full data. Pretty handler provides colored multi-line output.
- **CLI**: All terminal-facing logic lives in `internal/cli/`. `cmd/main.go` is a thin wrapper.
- **Server**: HTTP SSE API in `internal/server/`. API docs in `internal/server/API.md`.
- **Investigation**: Shared investigation logic in `internal/investigation/` (used by both CLI and server).
- **Prompts**: Markdown templates in `internal/prompts/`, loaded via Go embed. Template variables use `{{.var_name}}`.
- **Events**: Typed event constants in `internal/events/events.go`. Emit via `logger.Emit()`.
- **Storage**: All DB interaction through `internal/storage/Store` methods. Schema migrations in DDL const.

---

## Testing

Currently no automated test suite. Verify changes with:

```bash
go vet ./...
go build -o /dev/null ./cmd/
```

End-to-end testing requires:
1. Docker running with `pcapchu/sandbox:v1.0` image available
2. Valid `OPENAI_API_KEY` and `OPENAI_MODEL_NAME` set
3. A pcap file: `./pcapchu analyze --pcap capture.pcap`
