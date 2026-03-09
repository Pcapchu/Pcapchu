import { useState } from "react";
import { FileText, ChevronDown, ChevronUp, Clock, BarChart3 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Markdown } from "@/components/Markdown";

export function ReportMessage({ data }: { data: Record<string, unknown> }) {
  const [expanded, setExpanded] = useState(true);
  const round = Number(data.round || 0);
  const markdown = String(data.markdown_report || data.report || "");
  const totalSteps = Number(data.total_steps || 0);
  const durationMs = Number(data.duration_ms || 0);

  return (
    <div className="flex gap-3 px-4 py-3">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400">
        <FileText className="h-3.5 w-3.5" />
      </div>
      <div className="flex-1 min-w-0 space-y-2">
        <div className="flex items-center justify-between">
          <div>
            <p className="text-xs font-medium text-emerald-700 dark:text-emerald-400">
              Round {round} Report
            </p>
            <div className="flex gap-3 mt-0.5">
              {totalSteps > 0 && (
                <span className="flex items-center gap-1 text-[10px] text-muted-foreground">
                  <BarChart3 className="h-2.5 w-2.5" />
                  {totalSteps} steps
                </span>
              )}
              {durationMs > 0 && (
                <span className="flex items-center gap-1 text-[10px] text-muted-foreground">
                  <Clock className="h-2.5 w-2.5" />
                  {(durationMs / 1000).toFixed(1)}s
                </span>
              )}
            </div>
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? (
              <ChevronUp className="h-3.5 w-3.5" />
            ) : (
              <ChevronDown className="h-3.5 w-3.5" />
            )}
          </Button>
        </div>
        {expanded && markdown && (
          <div className="rounded-lg border bg-card p-4">
            <Markdown content={markdown} />
          </div>
        )}
      </div>
    </div>
  );
}
