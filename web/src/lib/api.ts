import type {
  UploadResponse,
  ReattachResponse,
  Session,
  SessionDetail,
  PcapFile,
  SSEEvent,
  EventType,
} from "./types";

const BASE = "";

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${url}`, init);
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${res.status}: ${text}`);
  }
  return res.json() as Promise<T>;
}

// --- Pcap ---

export async function uploadPcap(file: File): Promise<UploadResponse> {
  const form = new FormData();
  form.append("file", file);
  return fetchJSON<UploadResponse>("/api/pcap/upload", {
    method: "POST",
    body: form,
  });
}

export async function listPcapFiles(): Promise<PcapFile[]> {
  const body = await fetchJSON<{ pcap_files: PcapFile[] }>("/api/pcap");
  return body.pcap_files;
}

export async function deletePcapFile(id: number): Promise<void> {
  await fetch(`${BASE}/api/pcap/${id}`, { method: "DELETE" });
}

export async function reattachPcap(
  sessionId: string,
  pcapId: number
): Promise<ReattachResponse> {
  return fetchJSON<ReattachResponse>(
    `/api/sessions/${encodeURIComponent(sessionId)}/pcap`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pcap_id: pcapId }),
    }
  );
}

// --- Sessions ---

export async function listSessions(): Promise<Session[]> {
  const body = await fetchJSON<{ sessions: Session[] }>("/api/sessions");
  return body.sessions;
}

export async function getSession(sessionId: string): Promise<SessionDetail> {
  return fetchJSON<SessionDetail>(
    `/api/sessions/${encodeURIComponent(sessionId)}`
  );
}

export async function deleteSession(sessionId: string): Promise<void> {
  await fetch(`${BASE}/api/sessions/${encodeURIComponent(sessionId)}`, {
    method: "DELETE",
  });
}

// --- Event History ---

export async function loadSessionEvents(
  sessionId: string
): Promise<SSEEvent[]> {
  const body = await fetchJSON<{
    session_id: string;
    events: Array<{
      seq: number;
      type: string;
      data: Record<string, unknown>;
      timestamp: string;
    }>;
  }>(`/api/sessions/${encodeURIComponent(sessionId)}/events`);
  return body.events.map((e) => ({
    seq: e.seq,
    type: e.type as EventType,
    data: e.data,
    timestamp: e.timestamp,
  }));
}

// --- Analysis (SSE via fetch + ReadableStream) ---

/**
 * Start or continue an investigation. The response is an SSE stream.
 * Returns an AbortController that can be used to cancel (closing the
 * connection triggers server-side cancellation via r.Context().Done()).
 */
export function analyzeSession(
  sessionId: string,
  query: string,
  onEvent: (event: SSEEvent) => void,
  onDone: (status: string) => void,
  onError: (err: string) => void
): AbortController {
  const controller = new AbortController();

  const url = `${BASE}/api/sessions/${encodeURIComponent(sessionId)}/analyze`;

  fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query }),
    signal: controller.signal,
  })
    .then(async (res) => {
      if (!res.ok) {
        const text = await res.text();
        onError(`${res.status}: ${text}`);
        return;
      }
      if (!res.body) {
        onError("No response body");
        return;
      }
      await readSSEStream(res.body, onEvent, onDone, onError);
    })
    .catch((err) => {
      if (err instanceof DOMException && err.name === "AbortError") {
        onDone("cancelled");
        return;
      }
      onError(err instanceof Error ? err.message : String(err));
    });

  return controller;
}

/**
 * Parse an SSE stream from a ReadableStream<Uint8Array>.
 * Handles id:, event:, data: fields per the SSE spec.
 */
async function readSSEStream(
  body: ReadableStream<Uint8Array>,
  onEvent: (event: SSEEvent) => void,
  onDone: (status: string) => void,
  onError: (err: string) => void
): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let currentId = 0;
  let currentEvent = "";
  let currentData = "";

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      // Keep the last (potentially incomplete) line in the buffer
      buffer = lines.pop() ?? "";

      for (const line of lines) {
        if (line === "") {
          // Empty line = event dispatch
          if (currentEvent && currentData) {
            if (currentEvent === "done") {
              try {
                const d = JSON.parse(currentData);
                onDone(String(d.status || "completed"));
              } catch {
                onDone("completed");
              }
            } else {
              try {
                const data = JSON.parse(currentData);
                onEvent({
                  seq: currentId,
                  type: currentEvent as EventType,
                  data,
                });
              } catch {
                // skip malformed data
              }
            }
          }
          currentId = 0;
          currentEvent = "";
          currentData = "";
        } else if (line.startsWith("id: ")) {
          currentId = parseInt(line.slice(4), 10) || 0;
        } else if (line.startsWith("event: ")) {
          currentEvent = line.slice(7);
        } else if (line.startsWith("data: ")) {
          currentData = line.slice(6);
        } else if (line.startsWith(":")) {
          // comment (keepalive), ignore
        }
      }
    }
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") {
      return;
    }
    onError(err instanceof Error ? err.message : String(err));
  } finally {
    reader.releaseLock();
  }
}
