# Pcapchu HTTP SSE API Reference

Base URL: `http://localhost:8080` (configurable with `--addr`)

All responses use `Content-Type: application/json` unless noted otherwise.
CORS is enabled for all origins (`*`).

---

## Table of Contents

- [Quick Start](#quick-start)
- [Lifecycle](#lifecycle)
- [Analysis](#analysis)
  - [POST /api/analyze](#post-apianalyze)
  - [POST /api/sessions/{id}/continue](#post-apisessionsidcontinue)
  - [POST /api/sessions/{id}/cancel](#post-apisessionsidcancel)
- [Event Streaming](#event-streaming)
  - [GET /api/sessions/{id}/stream](#get-apisessionsidstream)
  - [GET /api/sessions/{id}/events](#get-apisessionsidevents)
- [Session Management](#session-management)
  - [GET /api/sessions](#get-apisessions)
  - [GET /api/sessions/{id}](#get-apisessionsid)
  - [DELETE /api/sessions/{id}](#delete-apisessionsid)
- [Pcap Management](#pcap-management)
  - [POST /api/pcap/upload](#post-apipcapupload)
  - [GET /api/pcap](#get-apipcap)
  - [DELETE /api/pcap/{id}](#delete-apipcapid)
  - [PATCH /api/sessions/{id}/pcap](#patch-apisessionsidpcap)
- [Event Types](#event-types)
- [Session Status](#session-status)

---

## Quick Start

```bash
# 1. Start the server
./pcapchu serve --addr :8080

# 2. Start analysis (upload pcap + query)
curl -X POST http://localhost:8080/api/analyze \
  -F "pcap=@capture.pcap" \
  -F "query=Find security threats" \
  -F "rounds=2"
# → {"session_id":"abc-123","status":"running"}

# 3. Connect to SSE stream
curl -N http://localhost:8080/api/sessions/abc-123/stream
# → event: analysis.started
# → data: {"session_id":"abc-123","total_rounds":2}
# → ...
# → event: done
# → data: {}
```

---

## Lifecycle

Investigation lifecycle per request:

```
POST /api/analyze
       │
       ├─► 201 {"session_id","status":"running"}
       │
       ▼
   Runner goroutine starts
       │
       ├── Create Docker sandbox
       ├── Copy pcap to container
       ├── Planner → Executor loop (per round)
       │      ├── Events broadcast to SSE clients
       │      └── Events persisted to DB (session_events)
       ├── analysis.completed / error
       └── Docker sandbox cleaned up
              │
              ▼
         SSE stream sends "done" event
         Runner goroutine exits
```

Key design points:

- **Investigation goroutine uses `context.Background()`** — it outlives the
  HTTP request that started it. The POST response returns immediately.
- **SSE stream = observation window** — connecting to `/stream` does not
  control the investigation lifecycle. You can connect/disconnect freely.
- **Cancel = explicit** — use `POST /sessions/{id}/cancel` to abort.
  This cancels the investigation context, triggers Docker cleanup.
- **Cleanup on goroutine exit** — `env.Cleanup()` always runs via `defer`
  when the investigation goroutine exits (success, error, or cancel).
- **Event replay** — all events are persisted to `session_events` with
  monotonic sequence numbers. Reconnecting to `/stream` or calling
  `/events` replays stored events from DB.

---

## Analysis

### POST /api/analyze

Start a new analysis session.

**Content-Type:** `multipart/form-data` or `application/json`

#### Multipart form fields

| Field | Type | Required | Description |
|---|---|---|---|
| `pcap` | file | Yes* | Pcap file upload |
| `pcap_id` | string | Yes* | OR reference an already-stored pcap by ID |
| `query` | string | No | Analysis query (default: generic security analysis) |
| `rounds` | string | No | Number of investigation rounds (default: `"1"`) |
| `store_pcap` | string | No | `"true"` to persist pcap blob in SQLite |

\*One of `pcap` (file) or `pcap_id` is required.

#### JSON body (alternative)

```json
{
  "pcap_id": 1,
  "query": "Find security threats",
  "rounds": 2,
  "store_pcap": true
}
```

#### Response — 201 Created

```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "running"
}
```

#### Behavior

1. If a pcap file is uploaded, it is always stored in SQLite (SHA-256 deduped).
2. A temp file is written for copying to the Docker container; cleaned up after
   the investigation finishes.
3. The investigation starts in a background goroutine.
4. An initial `session.created` event (seq=0) is saved to DB immediately.

#### Errors

| Status | Reason |
|---|---|
| 400 | No pcap provided (neither file nor pcap_id) |
| 409 | Session already running (shouldn't happen for new sessions) |
| 500 | pcap storage / temp file failure |

---

### POST /api/sessions/{id}/continue

Add more investigation rounds to an existing session.

**Content-Type:** `application/json`

```json
{
  "query": "Focus on DNS tunneling",
  "rounds": 1
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `query` | string | No | Override the original query for new rounds |
| `rounds` | int | No | Number of additional rounds (default: 1) |

#### Response — 200 OK

```json
{
  "session_id": "abc-123",
  "status": "running",
  "start_round": 3,
  "end_round": 3
}
```

#### Errors

| Status | Reason |
|---|---|
| 404 | Session not found |
| 409 | Session is already running |

---

### POST /api/sessions/{id}/cancel

Cancel an active investigation. Triggers context cancellation → Docker cleanup.

#### Response — 200 OK

```json
{
  "session_id": "abc-123",
  "status": "cancelled"
}
```

#### Errors

| Status | Reason |
|---|---|
| 404 | Session is not running |

---

## Event Streaming

### GET /api/sessions/{id}/stream

Server-Sent Events (SSE) endpoint.

**Content-Type:** `text/event-stream`

#### Reconnection

Supports `Last-Event-ID` header. On reconnect, only events with seq > last
received ID are sent.

#### Behavior

1. **Subscribe** to live event channel (if investigation is active).
2. **Replay** stored events from DB (skipping any ≤ `Last-Event-ID`).
3. **Stream** live events (deduped against already-sent seqs).
4. When investigation finishes, sends `event: done` and closes.
5. For already-finished sessions, replays all stored events then sends
   `event: done` immediately.
6. Keep-alive comments (`: keepalive`) sent every 15 seconds.

#### SSE Wire Format

```
id: 1
event: round.started
data: {"round":1,"total_rounds":2}

id: 2
event: step.started
data: {"step_id":1,"intent":"Analyze DNS traffic","total_steps":3}

...

event: done
data: {}
```

Each event has:
- `id:` — monotonic sequence number (for `Last-Event-ID` reconnect)
- `event:` — event type string
- `data:` — JSON payload

The terminal `done` event has no `id:` (seq=0).

---

### GET /api/sessions/{id}/events

Non-streaming JSON alternative. Returns all stored events for a session.

#### Response — 200 OK

```json
{
  "session_id": "abc-123",
  "events": [
    {
      "seq": 0,
      "type": "session.created",
      "data": {"session_id":"abc-123","user_query":"...","pcap_source":"db"},
      "timestamp": "2026-03-09T10:00:00Z"
    },
    {
      "seq": 1,
      "type": "analysis.started",
      "data": {"session_id":"abc-123","total_rounds":2},
      "timestamp": "2026-03-09T10:00:01Z"
    }
  ]
}
```

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
      "user_query": "Find security threats",
      "round_count": 2,
      "status": "completed",
      "pcap_source": "db",
      "created_at": "2026-03-09T10:00:00Z",
      "updated_at": "2026-03-09T10:05:00Z"
    }
  ]
}
```

`status` reflects the runner's in-memory state — if the runner considers the
session active, status is overridden to `"running"` regardless of DB value.

---

### GET /api/sessions/{id}

Get a single session with its investigation rounds.

#### Response — 200 OK

```json
{
  "id": "abc-123",
  "user_query": "Find security threats",
  "status": "completed",
  "round_count": 2,
  "rounds": [
    {
      "round": 1,
      "summary": "Found suspicious DNS queries...",
      "key_findings": "- DNS tunneling detected\n- ...",
      "open_questions": "Need to examine payload...",
      "markdown_report": "### \ud83c\udfaf Investigation Objectives ...\n\n...",
      "created_at": "2026-03-09T10:02:00Z"
    }
  ],
  "created_at": "2026-03-09T10:00:00Z",
  "updated_at": "2026-03-09T10:05:00Z"
}
```

#### Errors

| Status | Reason |
|---|---|
| 404 | Session not found |

---

### DELETE /api/sessions/{id}

Delete a session and all associated data (rounds, events, snapshots).
If the session is currently running, it is cancelled first.

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

Upload and store a pcap file.

**Content-Type:** `multipart/form-data`

| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | Yes | The pcap file |

#### Response — 201 Created

```json
{
  "id": 1,
  "filename": "capture.pcap",
  "size": 1048576
}
```

If an identical file (same SHA-256) is already stored, the existing ID is
returned.

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
`pcap_file_id` set to NULL (SQL `ON DELETE SET NULL`). Those sessions can
later have a new pcap re-attached via `PATCH /api/sessions/{id}/pcap`.

#### Response — 204 No Content

#### Errors

| Status | Reason |
|---|---|
| 404 | Pcap file not found |

---

### PATCH /api/sessions/{id}/pcap

Re-attach a pcap file to an existing session. Useful after deleting a pcap
and re-uploading a replacement, or pointing a session at a different pcap.

Cannot be called while the session is running.

**Content-Type:** `multipart/form-data` or `application/json`

#### Option A: Upload new file

```
PATCH /api/sessions/abc-123/pcap
Content-Type: multipart/form-data
[file field: the pcap file]
```

The file is stored in SQLite (SHA-256 deduped) and bound to the session.

#### Option B: Bind existing stored pcap

```json
{
  "pcap_id": 1
}
```

#### Response — 200 OK

```json
{
  "session_id": "abc-123",
  "pcap_id": 1
}
```

#### Errors

| Status | Reason |
|---|---|
| 400 | Missing pcap_id / invalid JSON |
| 404 | Session or pcap not found |
| 409 | Session is currently running |

---

## Event Types

Events emitted during an investigation, available via SSE stream and
the `/events` endpoint.

| Type | Data Fields | Description |
|---|---|---|
| `session.created` | session_id, user_query, pcap_source | Session initialized |
| `session.resumed` | session_id, from_round | Session continued |
| `analysis.started` | session_id, total_rounds | Investigation loop started |
| `analysis.completed` | session_id, total_rounds | Investigation loop finished |
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
| `info` | message | General info message |
| `error` | phase, message, step_id? | General error |
| `done` | *(empty)* | SSE-only: investigation finished, stream closing |

---

## Session Status

| Status | Description |
|---|---|
| `idle` | Created but not yet started (default) |
| `running` | Investigation in progress |
| `completed` | All rounds finished successfully |
| `error` | Investigation failed with an error |
| `cancelled` | Explicitly cancelled by user |
| `interrupted` | Server restarted while investigation was running |
