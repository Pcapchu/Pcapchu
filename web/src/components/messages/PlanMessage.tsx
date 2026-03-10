import { ListChecks, Lightbulb, CheckCircle2 } from "lucide-react";

interface PlanStep {
  step_id?: number;
  id?: number;
  intent: string;
}

export function PlanMessage({
  data,
  completedStepIds,
}: {
  data: Record<string, unknown>;
  completedStepIds?: Set<number>;
}) {
  const thought = String(data.thought || "");
  const steps = (data.steps || []) as PlanStep[];
  const totalSteps = Number(data.total_steps || steps.length);
  const completed = completedStepIds ?? new Set<number>();

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
          {steps.map((s) => {
            const stepId = s.step_id ?? s.id ?? 0;
            const done = completed.has(stepId);
            return (
              <div
                key={stepId}
                className="flex items-start gap-2 text-xs"
              >
                {done ? (
                  <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-green-600" />
                ) : (
                  <span className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border border-muted-foreground/40 text-[10px] font-semibold text-muted-foreground">
                    {stepId}
                  </span>
                )}
                <span className={done ? "text-muted-foreground line-through" : ""}>
                  {s.intent}
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
