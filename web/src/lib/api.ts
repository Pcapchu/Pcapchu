import type {
  AnalyzeResponse,
  ContinueResponse,
  Session,
  SessionDetail,
  PcapFile,
  SSEEvent,
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

// --- Analysis ---

export async function startAnalysis(
  pcap: File,
  query: string,
  rounds: number
): Promise<AnalyzeResponse> {
  const form = new FormData();
  form.append("pcap", pcap);
  form.append("query", query);
  form.append("rounds", String(rounds));
  form.append("store_pcap", "true");
  return fetchJSON<AnalyzeResponse>("/api/analyze", {
    method: "POST",
    body: form,
  });
}

export async function startAnalysisWithPcapId(
  pcapId: number,
  query: string,
  rounds: number
): Promise<AnalyzeResponse> {
  return fetchJSON<AnalyzeResponse>("/api/analyze", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ pcap_id: pcapId, query, rounds }),
  });
}

export async function continueSession(
  sessionId: string,
  query: string,
  rounds: number
): Promise<ContinueResponse> {
  return fetchJSON<ContinueResponse>(`/api/sessions/${encodeURIComponent(sessionId)}/continue`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query, rounds }),
  });
}

export async function cancelSession(sessionId: string): Promise<void> {
  await fetch(`${BASE}/api/sessions/${encodeURIComponent(sessionId)}/cancel`, { method: "POST" });
}

// --- Sessions ---

export async function listSessions(): Promise<Session[]> {
  const body = await fetchJSON<{ sessions: Session[] }>("/api/sessions");
  return body.sessions;
}

export async function getSession(sessionId: string): Promise<SessionDetail> {
  return fetchJSON<SessionDetail>(`/api/sessions/${encodeURIComponent(sessionId)}`);
}

export async function deleteSession(sessionId: string): Promise<void> {
  await fetch(`${BASE}/api/sessions/${encodeURIComponent(sessionId)}`, { method: "DELETE" });
}

// --- Pcap ---

export async function uploadPcap(file: File): Promise<PcapFile> {
  const form = new FormData();
  form.append("file", file);
  return fetchJSON<PcapFile>("/api/pcap/upload", {
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

// --- SSE ---

export function subscribeSSE(
  sessionId: string,
  onEvent: (event: SSEEvent) => void,
  onDone: () => void,
  onError?: (err: Event) => void,
  lastEventId?: number
): () => void {
  const url = `${BASE}/api/sessions/${encodeURIComponent(sessionId)}/stream`;
  const es = new EventSource(url);

  // For reconnection with Last-Event-ID, EventSource doesn't support custom
  // headers directly. We rely on the browser's built-in reconnect mechanism
  // which sends Last-Event-ID automatically.

  const eventTypes = [
    "session.created",
    "session.resumed",
    "analysis.started",
    "analysis.completed",
    "pcap.loaded",
    "round.started",
    "round.completed",
    "plan.created",
    "plan.error",
    "step.started",
    "step.findings",
    "step.completed",
    "step.error",
    "report.generated",
    "info",
    "error",
  ];

  for (const type of eventTypes) {
    es.addEventListener(type, (e: MessageEvent) => {
      const seq = e.lastEventId ? parseInt(e.lastEventId, 10) : 0;
      if (lastEventId && seq <= lastEventId) return;
      try {
        onEvent({
          seq,
          type: type as SSEEvent["type"],
          data: JSON.parse(e.data),
        });
      } catch {
        // ignore parse errors
      }
    });
  }

  es.addEventListener("done", () => {
    onDone();
    es.close();
  });

  es.onerror = (err) => {
    onError?.(err);
  };

  return () => es.close();
}
