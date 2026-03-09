import { Loader2, CheckCircle2, Search } from "lucide-react";

interface StepMessageProps {
  type: "step.started" | "step.findings" | "step.completed";
  data: Record<string, unknown>;
}

export function StepMessage({ type, data }: StepMessageProps) {
  const stepId = Number(data.step_id || 0);
  const intent = String(data.intent || "");

  if (type === "step.started") {
    return (
      <div className="flex gap-3 px-4 py-2">
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-400">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        </div>
        <div className="flex-1 min-w-0 pt-1">
          <p className="text-xs">
            <span className="font-medium text-sky-700 dark:text-sky-400">Step {stepId}</span>
            <span className="text-muted-foreground"> — {intent}</span>
          </p>
        </div>
      </div>
    );
  }

  if (type === "step.findings") {
    const findings = String(data.findings || "");
    return (
      <div className="flex gap-3 px-4 py-2">
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-400">
          <Search className="h-3.5 w-3.5" />
        </div>
        <div className="flex-1 min-w-0 space-y-1">
          <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
            Step {stepId} Findings
          </p>
          {findings && (
            <div className="rounded-md border bg-muted/30 p-2 text-xs text-foreground whitespace-pre-wrap">
              {findings.length > 800 ? findings.slice(0, 800) + "..." : findings}
            </div>
          )}
        </div>
      </div>
    );
  }

  // step.completed
  const totalSteps = Number(data.total_steps || 0);
  return (
    <div className="flex justify-center px-4 py-1">
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <CheckCircle2 className="h-3 w-3 text-green-600" />
        Step {stepId}/{totalSteps} completed
      </div>
    </div>
  );
}
