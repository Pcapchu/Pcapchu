import { RotateCcw, CheckCircle2 } from "lucide-react";

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

  // round.completed — just a minimal divider, report is shown via report.generated
  return (
    <div className="flex justify-center px-4 py-1">
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <CheckCircle2 className="h-3 w-3 text-green-600" />
        Round {round} completed
      </div>
    </div>
  );
}
