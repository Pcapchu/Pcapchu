import { RotateCcw, CheckCircle2 } from "lucide-react";
import { Markdown } from "@/components/Markdown";

interface RoundMessageProps {
  type: "start" | "end";
  data: Record<string, unknown>;
}

export function RoundMessage({ type, data }: RoundMessageProps) {
  const round = Number(data.round || 0);
  const totalRounds = Number(data.total_rounds || 0);

  if (type === "start") {
    return (
      <div className="flex justify-center px-4 py-2">
        <div className="flex items-center gap-2 rounded-full border bg-background px-4 py-1.5 shadow-sm">
          <RotateCcw className="h-3.5 w-3.5 text-indigo-600" />
          <span className="text-xs font-medium">
            Round {round}
            {totalRounds > 0 && ` / ${totalRounds}`}
          </span>
        </div>
      </div>
    );
  }

  // round.completed
  const summary = String(data.summary || "");
  const keyFindings = String(data.key_findings || "");
  const markdownReport = String(data.markdown_report || "");

  return (
    <div className="flex gap-3 px-4 py-3">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-green-100 text-green-700 dark:bg-green-950 dark:text-green-400">
        <CheckCircle2 className="h-3.5 w-3.5" />
      </div>
      <div className="flex-1 min-w-0 space-y-1">
        <p className="text-xs font-medium text-green-700 dark:text-green-400">
          Round {round} Completed
        </p>
        {summary && !markdownReport && (
          <p className="text-xs text-muted-foreground">{summary}</p>
        )}
        {markdownReport && (
          <div className="mt-2 rounded-lg border bg-card p-4">
            <Markdown content={markdownReport} />
          </div>
        )}
        {keyFindings && !markdownReport && (
          <div className="mt-1 rounded-md bg-muted/50 p-2">
            <Markdown content={keyFindings} />
          </div>
        )}
      </div>
    </div>
  );
}
