import { useEffect, useRef, useMemo } from "react";
import { Layers, Loader2 } from "lucide-react";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ChatMessage } from "@/components/ChatMessage";
import { useStore } from "@/lib/store";

const emptySet = new Set<number>();

export function ChatArea() {
  const events = useStore((s) => s.events);
  const loading = useStore((s) => s.loading);
  const sessionActive = useStore((s) => s.sessionActive);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [events.length]);

  // Derive per-round completed step ID sets + per-event round assignment.
  const { eventRounds, completedByRound } = useMemo(() => {
    // Map from round number → set of completed step IDs
    const completed = new Map<number, Set<number>>();
    // Map from round number → set of all seen step IDs (for round.completed flush)
    const seenByRound = new Map<number, Set<number>>();
    // Round assignment for each event index
    const rounds: number[] = [];
    let currentRound = 0;

    for (const ev of events) {
      if (ev.type === "round.started") {
        currentRound = Number(ev.data.round || 0);
      }
      rounds.push(currentRound);

      if (ev.type === "step.started" || ev.type === "step.findings") {
        const stepId = Number(ev.data.step_id || 0);
        if (!seenByRound.has(currentRound)) seenByRound.set(currentRound, new Set());
        seenByRound.get(currentRound)!.add(stepId);
      } else if (ev.type === "step.completed") {
        const stepId = Number(ev.data.step_id || 0);
        if (!completed.has(currentRound)) completed.set(currentRound, new Set());
        completed.get(currentRound)!.add(stepId);
      } else if (ev.type === "round.completed") {
        const round = Number(ev.data.round || currentRound);
        // Mark all steps in this round as completed
        const seen = seenByRound.get(round);
        if (seen) {
          if (!completed.has(round)) completed.set(round, new Set());
          const set = completed.get(round)!;
          for (const id of seen) set.add(id);
        }
      }
    }

    return { eventRounds: rounds, completedByRound: completed };
  }, [events]);

  // Empty state — welcome screen
  if (events.length === 0 && !loading) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-4 p-8">
        <div className="flex flex-col items-center gap-3">
          <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-primary/10">
            <Layers className="h-8 w-8 text-primary" />
          </div>
          <h1 className="text-2xl font-bold tracking-tight">Pcapchu</h1>
          <p className="max-w-sm text-center text-sm text-muted-foreground">
            AI-powered network packet capture investigation.
            Attach a .pcap file and describe what you want to investigate.
          </p>
        </div>
      </div>
    );
  }

  return (
    <ScrollArea className="flex-1">
      <div className="mx-auto max-w-3xl py-4 space-y-1">
        {events.map((ev, i) => (
          <ChatMessage
            key={i}
            event={ev}
            completedStepIds={completedByRound.get(eventRounds[i]) ?? emptySet}
          />
        ))}

        {/* Loading indicator while investigation is running */}
        {sessionActive && (
          <div className="flex justify-center py-3">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              Investigating...
            </div>
          </div>
        )}

        <div ref={bottomRef} />
      </div>
    </ScrollArea>
  );
}
