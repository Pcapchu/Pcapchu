import { useState, useCallback, useRef, useEffect } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Sidebar } from "@/components/Sidebar";
import { ChatArea } from "@/components/ChatArea";
import { ChatInput } from "@/components/ChatInput";
import { PcapManager } from "@/components/PcapManager";
import {
  startAnalysis,
  continueSession,
  cancelSession,
  subscribeSSE,
  getSession,
} from "@/lib/api";
import type { SSEEvent } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { StopCircle } from "lucide-react";

export default function App() {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [pcapManagerOpen, setPcapManagerOpen] = useState(false);

  const [currentSessionId, setCurrentSessionId] = useState<string | null>(null);
  const [events, setEvents] = useState<SSEEvent[]>([]);
  const [sessionActive, setSessionActive] = useState(false);
  const [loading, setLoading] = useState(false);
  const [sidebarRefreshKey, setSidebarRefreshKey] = useState(0);

  const unsubRef = useRef<(() => void) | null>(null);

  // Clean up SSE on unmount
  useEffect(() => {
    return () => unsubRef.current?.();
  }, []);

  const connectSSE = useCallback((sessionId: string) => {
    // Unsubscribe previous
    unsubRef.current?.();
    setSessionActive(true);

    const unsub = subscribeSSE(
      sessionId,
      (ev) => setEvents((prev) => [...prev, ev]),
      () => {
        setSessionActive(false);
        setSidebarRefreshKey((k) => k + 1);
      },
      () => {
        // on error - could try reconnect, just mark inactive for now
        setSessionActive(false);
      }
    );
    unsubRef.current = unsub;
  }, []);

  const handleSelectSession = useCallback(
    async (id: string) => {
      // Unsubscribe from previous session
      unsubRef.current?.();
      setCurrentSessionId(id);
      setEvents([]);
      setSessionActive(false);

      // Load existing session data and connect SSE
      try {
        const session = await getSession(id);
        // If session is running, connect SSE for live events
        if (session.status === "running") {
          connectSSE(id);
        } else {
          // Load stored events for completed sessions - reconstruct from rounds
          const reconstructed: SSEEvent[] = [];
          reconstructed.push({
            seq: 0,
            type: "session.created",
            data: {
              session_id: id,
              user_query: session.user_query,
              pcap_source: "",
            },
          });
          if (session.rounds) {
            for (const round of session.rounds) {
              if (round.markdown_report) {
                reconstructed.push({
                  seq: 0,
                  type: "report.generated",
                  data: {
                    round: round.round,
                    markdown_report: round.markdown_report,
                    report: round.summary || "",
                    total_steps: 0,
                    duration_ms: 0,
                    content_length: round.markdown_report.length,
                  },
                });
              } else if (round.summary || round.key_findings) {
                reconstructed.push({
                  seq: 0,
                  type: "round.completed",
                  data: {
                    round: round.round,
                    summary: round.summary,
                    key_findings: round.key_findings,
                  },
                });
              }
            }
          }
          reconstructed.push({
            seq: 0,
            type: "analysis.completed",
            data: { session_id: id, total_rounds: session.round_count },
          });
          setEvents(reconstructed);
        }
      } catch {
        // Session might not exist yet if we just created it
        connectSSE(id);
      }
    },
    [connectSSE]
  );

  const handleSend = useCallback(
    async (query: string, file: File | null, rounds: number) => {
      setLoading(true);
      try {
        if (currentSessionId && !file) {
          // Continue existing session
          await continueSession(
            currentSessionId,
            query || "Continue the investigation",
            rounds
          );
          // Reconnect SSE
          setSessionActive(true);
          connectSSE(currentSessionId);
        } else if (file) {
          // New session with file upload
          const res = await startAnalysis(
            file,
            query || "Analyze this pcap file and identify any security concerns.",
            rounds
          );
          setCurrentSessionId(res.session_id);
          setEvents([]);
          connectSSE(res.session_id);
          setSidebarRefreshKey((k) => k + 1);
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : "Failed to start";
        setEvents((prev) => [
          ...prev,
          {
            seq: 0,
            type: "error",
            data: { phase: "client", message },
          },
        ]);
      } finally {
        setLoading(false);
      }
    },
    [currentSessionId, connectSSE]
  );

  const handleCancel = useCallback(async () => {
    if (!currentSessionId) return;
    await cancelSession(currentSessionId);
    unsubRef.current?.();
    setSessionActive(false);
    setEvents((prev) => [
      ...prev,
      { seq: 0, type: "info", data: { message: "Investigation cancelled" } },
    ]);
    setSidebarRefreshKey((k) => k + 1);
  }, [currentSessionId]);

  const handleNewSession = useCallback(() => {
    unsubRef.current?.();
    setCurrentSessionId(null);
    setEvents([]);
    setSessionActive(false);
  }, []);

  return (
    <TooltipProvider delayDuration={300}>
      <div className="flex h-screen overflow-hidden">
        {/* Sidebar */}
        <Sidebar
          currentSessionId={currentSessionId}
          onSelectSession={handleSelectSession}
          onNewSession={handleNewSession}
          collapsed={sidebarCollapsed}
          onToggle={() => setSidebarCollapsed((v) => !v)}
          refreshKey={sidebarRefreshKey}
        />

        {/* Main chat area */}
        <div className="flex flex-1 flex-col min-w-0">
          {/* Top bar */}
          <header className="flex h-14 items-center justify-between border-b px-4">
            <div className="flex items-center gap-2 min-w-0">
              <h2 className="text-sm font-semibold truncate">
                {currentSessionId ? `Session ${currentSessionId.slice(0, 8)}...` : "Pcapchu"}
              </h2>
            </div>
            {sessionActive && (
              <Button variant="ghost" size="sm" onClick={handleCancel} className="text-destructive">
                <StopCircle className="h-3.5 w-3.5 mr-1" />
                Cancel
              </Button>
            )}
          </header>

          {/* Chat messages */}
          <ChatArea
            events={events}
            loading={loading}
            sessionActive={sessionActive}
          />

          {/* Input */}
          <ChatInput
            onSend={handleSend}
            onOpenSettings={() => setPcapManagerOpen(true)}
            disabled={loading}
            loading={loading}
            placeholder={
              currentSessionId
                ? "Ask a follow-up question..."
                : "Describe your investigation..."
            }
          />
        </div>

        {/* Pcap manager dialog */}
        <PcapManager
          open={pcapManagerOpen}
          onClose={() => setPcapManagerOpen(false)}
        />
      </div>
    </TooltipProvider>
  );
}
