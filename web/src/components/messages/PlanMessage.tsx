import { ListChecks, Lightbulb } from "lucide-react";

interface PlanStep {
  id: number;
  intent: string;
}

export function PlanMessage({ data }: { data: Record<string, unknown> }) {
  const thought = String(data.thought || "");
  const steps = (data.steps || []) as PlanStep[];
  const totalSteps = Number(data.total_steps || steps.length);

  return (
    <div className="flex gap-3 px-4 py-3">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-400">
        <ListChecks className="h-3.5 w-3.5" />
      </div>
      <div className="flex-1 min-w-0 space-y-2">
        <p className="text-xs font-medium text-purple-700 dark:text-purple-400">
          Investigation Plan — {totalSteps} step{totalSteps !== 1 ? "s" : ""}
        </p>
        {thought && (
          <div className="flex items-start gap-1.5 rounded-md bg-muted/50 p-2 text-xs text-muted-foreground">
            <Lightbulb className="mt-0.5 h-3 w-3 shrink-0 text-amber-500" />
            <p>{thought}</p>
          </div>
        )}
        <div className="space-y-1">
          {steps.map((s) => (
            <div
              key={s.id}
              className="flex items-start gap-2 text-xs"
            >
              <span className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-muted text-[10px] font-semibold text-muted-foreground">
                {s.id}
              </span>
              <span>{s.intent}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
