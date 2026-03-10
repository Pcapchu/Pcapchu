import { useState } from "react";
import { Loader2, CheckCircle2, Search, ChevronDown, ChevronRight } from "lucide-react";

const TRUNCATE_LEN = 600;

function ExpandableText({ text, label }: { text: string; label?: string }) {
  const needsTruncate = text.length > TRUNCATE_LEN;
  const [expanded, setExpanded] = useState(false);
  const display = !needsTruncate || expanded ? text : text.slice(0, TRUNCATE_LEN) + "…";

  return (
    <div className="rounded-md border bg-muted/30 p-2 text-xs text-foreground">
      {label && (
        <p className="mb-1 font-medium text-muted-foreground">{label}</p>
      )}
      <div className="whitespace-pre-wrap">{display}</div>
      {needsTruncate && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 flex items-center gap-0.5 text-[11px] font-medium text-primary hover:underline"
        >
          {expanded ? (
            <>
              <ChevronDown className="h-3 w-3" /> Show less
            </>
          ) : (
            <>
              <ChevronRight className="h-3 w-3" /> Show more ({text.length} chars)
            </>
          )}
        </button>
      )}
    </div>
  );
}

interface StepMessageProps {
  type: "step.started" | "step.findings" | "step.completed";
  data: Record<string, unknown>;
  completedStepIds?: Set<number>;
}

export function StepMessage({ type, data, completedStepIds }: StepMessageProps) {
  const stepId = Number(data.step_id || 0);
  const intent = String(data.intent || "");
  const completed = completedStepIds ?? new Set<number>();
  const isDone = completed.has(stepId);

  if (type === "step.started") {
    return (
      <div className="flex gap-3 px-4 py-2">
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-400">
          {isDone ? (
            <CheckCircle2 className="h-3.5 w-3.5 text-green-600" />
          ) : (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          )}
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
    const actions = String(data.actions || "");
    return (
      <div className="flex gap-3 px-4 py-2">
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-400">
          <Search className="h-3.5 w-3.5" />
        </div>
        <div className="flex-1 min-w-0 space-y-1.5">
          <p className="text-xs font-medium text-amber-700 dark:text-amber-400">
            Step {stepId} Findings
          </p>
          {findings && <ExpandableText text={findings} />}
          {actions && <ExpandableText text={actions} label="Actions" />}
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
