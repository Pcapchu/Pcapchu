import { useEffect } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Sidebar } from "@/components/Sidebar";
import { ChatArea } from "@/components/ChatArea";
import { ChatInput } from "@/components/ChatInput";
import { PcapManager } from "@/components/PcapManager";
import { useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import { StopCircle } from "lucide-react";

export default function App() {
  const currentSessionId = useStore((s) => s.currentSessionId);
  const sessionActive = useStore((s) => s.sessionActive);
  const handleCancel = useStore((s) => s.handleCancel);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      const ctrl = useStore.getState()._abortController;
      ctrl?.abort();
    };
  }, []);

  return (
    <TooltipProvider delayDuration={300}>
      <div className="flex h-screen overflow-hidden">
        <Sidebar />

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

          <ChatArea />
          <ChatInput />
        </div>

        <PcapManager />
      </div>
    </TooltipProvider>
  );
}
