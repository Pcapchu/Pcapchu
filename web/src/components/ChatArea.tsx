import { useEffect, useRef } from "react";
import { Layers, Loader2 } from "lucide-react";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ChatMessage } from "@/components/ChatMessage";
import type { SSEEvent } from "@/lib/types";

interface ChatAreaProps {
  events: SSEEvent[];
  loading?: boolean;
  sessionActive?: boolean;
}

export function ChatArea({ events, loading, sessionActive }: ChatAreaProps) {
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [events.length]);

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
          <ChatMessage key={i} event={ev} />
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
