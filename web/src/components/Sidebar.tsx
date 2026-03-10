import { useState, useEffect } from "react";
import {
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Trash2,
  MessageSquare,
  Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { listSessions, deleteSession } from "@/lib/api";
import { useStore } from "@/lib/store";
import type { Session, SessionStatus } from "@/lib/types";

const statusDot: Record<SessionStatus, string> = {
  idle: "bg-gray-400",
  running: "bg-green-500 animate-pulse",
  completed: "bg-blue-500",
  error: "bg-red-500",
  cancelled: "bg-gray-400",
  interrupted: "bg-orange-500",
};

export function Sidebar() {
  const currentSessionId = useStore((s) => s.currentSessionId);
  const onSelectSession = useStore((s) => s.handleSelectSession);
  const onNewSession = useStore((s) => s.handleNewSession);
  const collapsed = useStore((s) => s.sidebarCollapsed);
  const onToggle = useStore((s) => s.toggleSidebar);
  const refreshKey = useStore((s) => s.sidebarRefreshKey);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    listSessions()
      .then((data) => setSessions(data))
      .finally(() => setLoading(false));
  }, [refreshKey]);

  const handleDelete = async (e: React.MouseEvent, id: string) => {
    e.stopPropagation();
    await deleteSession(id);
    setSessions((prev) => prev.filter((s) => s.id !== id));
  };

  if (collapsed) {
    return (
      <div className="flex flex-col items-center border-r bg-muted/30 py-3 px-1.5 gap-2">
        <Button variant="ghost" size="icon" onClick={onToggle} className="h-8 w-8">
          <PanelLeftOpen className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" onClick={onNewSession} className="h-8 w-8">
          <Plus className="h-4 w-4" />
        </Button>
      </div>
    );
  }

  return (
    <div className="flex w-64 flex-col border-r bg-muted/30">
      {/* Header */}
      <div className="flex h-14 items-center justify-between border-b px-3">
        <span className="text-sm font-semibold">Sessions</span>
        <div className="flex gap-0.5">
          <Button variant="ghost" size="icon" onClick={onNewSession} className="h-7 w-7">
            <Plus className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" onClick={onToggle} className="h-7 w-7">
            <PanelLeftClose className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* Session list */}
      <ScrollArea className="flex-1">
        <div className="p-2 space-y-0.5">
          {loading && (
            <div className="flex justify-center py-6">
              <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
            </div>
          )}
          {!loading && sessions.length === 0 && (
            <p className="px-2 py-6 text-center text-xs text-muted-foreground">
              No sessions yet
            </p>
          )}
          {sessions.map((s) => (
            <button
              key={s.id}
              onClick={() => onSelectSession(s.id)}
              className={cn(
                "group flex w-full items-start gap-2 rounded-md px-2 py-2 text-left text-sm transition-colors",
                currentSessionId === s.id
                  ? "bg-accent text-accent-foreground"
                  : "hover:bg-accent/50 text-muted-foreground hover:text-foreground"
              )}
            >
              <MessageSquare className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <div className="flex-1 min-w-0">
                <p className="truncate text-xs font-medium leading-snug">
                  {s.user_query.length > 50
                    ? s.user_query.slice(0, 50) + "..."
                    : s.user_query}
                </p>
                <div className="mt-0.5 flex items-center gap-1.5">
                  <span className={cn("h-1.5 w-1.5 rounded-full", statusDot[s.status])} />
                  <span className="text-[10px] text-muted-foreground">
                    {s.round_count}R · {new Date(s.created_at).toLocaleDateString()}
                  </span>
                </div>
              </div>
              <Button
                variant="ghost"
                size="icon"
                className="h-5 w-5 opacity-0 group-hover:opacity-100 shrink-0"
                onClick={(e) => handleDelete(e, s.id)}
              >
                <Trash2 className="h-3 w-3" />
              </Button>
            </button>
          ))}
        </div>
      </ScrollArea>
    </div>
  );
}
