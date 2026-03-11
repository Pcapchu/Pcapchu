# Pcapchu HTTP SSE API Reference

Base URL: `http://localhost:8080` (configurable with `--addr`)

Responses use `Content-Type: application/json` for CRUD endpoints and
`Content-Type: text/event-stream` for analysis endpoints.
CORS is enabled for all origins (`*`).

---

## Table of Contents

- [Quick Start](#quick-start)
- [Lifecycle](#lifecycle)
- [Analysis (SSE)](#analysis-sse)
  - [POST /api/sessions/{id}/analyze](#post-apisessionsidanalyze)
- [Session Management](#session-management)
  - [GET /api/sessions](#get-apisessions)
  - [GET /api/sessions/{id}](#get-apisessionsid)
  - [DELETE /api/sessions/{id}](#delete-apisessionsid)
- [Pcap Management](#pcap-management)
  - [POST /api/pcap/upload](#post-apipcapupload)
  - [GET /api/pcap](#get-apipcap)
  - [DELETE /api/pcap/{id}](#delete-apipcapid)
- [Event Types](#event-types)
- [Session Status](#session-status)

---

## Quick Start

```bash
# 1. Start the server
./pcapchu serve --addr :8080

# 2. Upload pcap — creates a session automatically
curl -X POST http://localhost:8080/api/pcap/upload \
  -F "file=@capture.pcap"
# → {"session_id":"abc-123","pcap_id":1,"filename":"capture.pcap","size":1048576}

# 3. Start analysis — response is an SSE stream
curl -N -X POST http://localhost:8080/api/sessions/abc-123/analyze \
  -H "Content-Type: application/json" \
  -d '{"query":"Find security threats"}'
# → event: session.created
# → data: {"session_id":"abc-123","user_query":"Find security threats","pcap_source":"db"}
# → ...
# → event: analysis.completed
# → data: {"session_id":"abc-123","total_rounds":1}
# → event: done
# → data: {"session_id":"abc-123","status":"completed"}

# 4. Run another round (same endpoint, auto-detects continuation)
curl -N -X POST http://localhost:8080/api/sessions/abc-123/analyze \
  -H "Content-Type: application/json" \
  -d '{"query":"Focus on DNS tunneling"}'

# 5. Retrieve session detail with per-round history
curl http://localhost:8080/api/sessions/abc-123
```

---

## Lifecycle

The frontend workflow is:

1. **Upload pcap** → `POST /api/pcap/upload` → returns `session_id`
2. **Start analysis** → `POST /api/sessions/{id}/analyze` → SSE stream
3. **Continue (more rounds)** → same endpoint → SSE stream
4. **Load history** → `GET /api/sessions/{id}` → rounds with events

Each analysis request is **self-contained** — the POST response is an SSE
stream that stays open until the investigation completes (or the client
disconnects). There is no global server state tracking running investigations.

**Transactional persistence**: No data is written to the database during
the SSE stream. A `txCollector` buffers all events and round results in
memory. Only on successful completion (or error) is the data flushed
atomically. If the client disconnects mid-stream, nothing is persisted.

```
POST /api/pcap/upload
       │
       ├── Store pcap in DB (SHA-256 dedup)
       ├── Create session with pcap_file_id
       └── Return {session_id, pcap_id, filename, size}

POST /api/sessions/{id}/analyze  (SSE response)
       │
       ├── Load session, verify pcap_file_id exists
       ├── Auto-detect first-run vs continuation (by round count)
       ├── Create Runtime (Docker sandbox, LLM, agents)
       ├── Copy pcap from DB to container
       ├── Emit session.created / session.resumed
       ├── Planner → Executor loop (per round)
       │      └── Events streamed to SSE client (buffered in memory)
       ├── On completion: flush all data to DB atomically
       │      ├── session_events (tagged with round number)
       │      ├── rounds table
       │      ├── round_queries table
       │      └── session title + status
       ├── analysis.completed / error
       ├── Docker sandbox cleaned up
       └── SSE sends "done" event → connection closes
```

Key design:

- **One Runtime per request** — each analysis request creates its own
  `investigation.Runtime` (same as CLI), runs the investigation
  synchronously within the SSE response, then tears down.
- **Atomic persistence** — nothing is written to the database during the
  SSE stream. A `txCollector` buffers events and round data, flushing
  everything on completion. Client disconnect = no partial state.
- **Client disconnect = cancel** — when the client closes the connection,
  `r.Context()` is cancelled, which propagates to the runtime and triggers
  Docker cleanup. No data is persisted.
- **No global state** — there is no runner, no active session map, no
  goroutine pool. Each request is fully independent.
- **Unified endpoint** — `POST /api/sessions/{id}/analyze` handles both
  first analysis and continuation. It auto-detects by counting existing
  rounds in the DB.

---

## Analysis (SSE)

### POST /api/sessions/{id}/analyze

Start or continue an investigation on an existing session and stream events
via SSE. Each call runs exactly **one round**. The session must already have
a pcap attached (created via `POST /api/pcap/upload`).

The start round is auto-detected by counting existing rounds in the database.
Call this endpoint repeatedly to run additional rounds.

**Content-Type (request):** `application/json`
**Content-Type (response):** `text/event-stream`

#### JSON body

```json
{
  "query": "Find security threats"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `query` | string | **Yes** | Analysis query for this round |

#### Response — SSE stream

```
id: 1
event: session.created
data: {"session_id":"abc-123","user_query":"Find security threats","pcap_source":"db"}

id: 2
event: round.started
data: {"round":1,"total_rounds":1}

...

id: 25
event: analysis.completed
data: {"session_id":"abc-123","total_rounds":1}

event: done
data: {"session_id":"abc-123","status":"completed"}
```

The stream closes after the `done` event. If an error occurs, an `error`
event is sent before `done`.

On first analysis (`round == 1`), a `session.created` event is emitted.
On continuation (`round > 1`), a `session.resumed` event is emitted.

#### Cancellation

Close the HTTP connection to cancel the investigation. The server will:
1. Cancel the runtime context
2. Clean up the Docker sandbox
3. **Not persist any data** (no partial state in DB)

#### Errors

| Status | Reason |
|---|---|
| 400 | Invalid JSON / empty query / session has no pcap attached |
| 404 | Session not found |
| 500 | Streaming not supported |

Errors after SSE has started are delivered as `error` events in the stream.

---

## Session Management

### GET /api/sessions

List all sessions.

#### Response — 200 OK

```json
{
  "sessions": [
    {
      "id": "abc-123",
      "session_title": "Find security threats",
      "round_count": 2,
      "status": "completed",
      "pcap_source": "db",
      "created_at": "2026-03-09T10:00:00Z",
      "updated_at": "2026-03-09T10:05:00Z"
    }
  ]
}
```

`session_title` is set from the first round's query. `status` is read
directly from the database.

---

### GET /api/sessions/{id}

Get a single session with its investigation history organized by round.
Each round contains the user query for that round and all events that
occurred during it.

#### Response — 200 OK

```json
{
  "id": "abc-123",
  "session_title": "Find security threats",
  "status": "completed",
  "round_count": 2,
  "rounds": [
    {
      "round": 1,
      "user_query": "Find security threats",
      "events": [
        {
          "seq": 1,
          "type": "session.created",
          "data": {"session_id":"abc-123","user_query":"Find security threats","pcap_source":"db"},
          "timestamp": "2026-03-09T10:00:01Z"
        },
        {
          "seq": 2,
          "type": "round.started",
          "data": {"round":1,"total_rounds":1},
          "timestamp": "2026-03-09T10:00:02Z"
        }
      ]
    }
  ],
  "created_at": "2026-03-09T10:00:00Z",
  "updated_at": "2026-03-09T10:05:00Z"
}
```

Events within each round are ordered by `seq`. The `user_query` field on
each round comes from the `round_queries` table.

#### Errors

| Status | Reason |
|---|---|
| 404 | Session not found |

---

### DELETE /api/sessions/{id}

Delete a session and all associated data (rounds, events, snapshots, queries).

#### Response — 204 No Content

#### Errors

| Status | Reason |
|---|---|
| 404 | Session not found |

---

## Pcap Management

Pcap files are stored as BLOBs in SQLite, deduplicated by SHA-256.
When you upload a file whose hash already exists, the existing row
is returned (no duplicate storage).

### POST /api/pcap/upload

Upload a pcap file and create a new session bound to it.

**Content-Type:** `multipart/form-data`

| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | Yes | The pcap file |

#### Response — 201 Created

```json
{
  "session_id": "abc-123",
  "pcap_id": 1,
  "filename": "capture.pcap",
  "size": 1048576
}
```

If an identical file (same SHA-256) is already stored, the existing pcap ID
is reused (no duplicate storage). A new session is always created.

---

### GET /api/pcap

List all stored pcap files (metadata only, no blob data).

#### Response — 200 OK

```json
{
  "pcap_files": [
    {
      "id": 1,
      "filename": "capture.pcap",
      "size": 1048576,
      "sha256": "a1b2c3d4...",
      "created_at": "2026-03-09T09:00:00Z"
    }
  ]
}
```

---

### DELETE /api/pcap/{id}

Remove a stored pcap file. Sessions referencing this pcap will have their
`pcap_file_id` set to NULL (SQL `ON DELETE SET NULL`).

#### Response — 204 No Content

#### Errors

| Status | Reason |
|---|---|
| 404 | Pcap file not found |

---

## Event Types

Events emitted during an investigation, available via SSE stream and
the session detail endpoint.

| Type | Data Fields | Description |
|---|---|---|
| `session.created` | session_id, user_query, pcap_source | Session initialized (round 1) |
| `session.resumed` | session_id, from_round | Session continued (round 2+) |
| `pcap.loaded` | source, path, size, filename | Pcap copied to sandbox |
| `round.started` | round, total_rounds | Round N begins |
| `round.completed` | round, summary, key_findings, markdown_report | Round N finished |
| `plan.created` | thought, total_steps, steps[] | Planner produced a plan |
| `plan.error` | phase, message, step_id? | Planner failed |
| `step.started` | step_id, intent, total_steps | Executor step begins |
| `step.findings` | step_id, intent, findings, actions | Step produced findings |
| `step.completed` | step_id, total_steps | Executor step finished |
| `step.error` | phase, message, step_id? | Executor step failed |
| `report.generated` | round, report, markdown_report, content_length, total_steps, duration_ms | Round summary generated |
| `analysis.completed` | session_id, total_rounds | Investigation loop finished |
| `info` | message | General info message |
| `error` | phase, message, step_id? | General error |
| `done` | session_id, status | SSE-only: investigation finished, stream closing |

---

## Session Status

| Status | Description |
|---|---|
| `idle` | Created but not yet started (default) |
| `running` | Investigation in progress |
| `completed` | All rounds finished successfully |
| `error` | Investigation failed with an error |
| `cancelled` | Client disconnected during investigation |
