import { AlertTriangle } from "lucide-react";

export function ErrorMessage({ data }: { data: Record<string, unknown> }) {
  const phase = String(data.phase || "");
  const message = String(data.message || "Unknown error");
  const stepId = data.step_id ? Number(data.step_id) : null;

  return (
    <div className="flex gap-3 px-4 py-2">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-400">
        <AlertTriangle className="h-3.5 w-3.5" />
      </div>
      <div className="flex-1 min-w-0 rounded-md border border-red-200 bg-red-50 p-2 dark:border-red-900 dark:bg-red-950/50">
        <p className="text-xs font-medium text-red-700 dark:text-red-400">
          Error{phase ? ` (${phase})` : ""}
          {stepId ? ` — Step ${stepId}` : ""}
        </p>
        <p className="mt-0.5 text-xs text-red-600 dark:text-red-300">{message}</p>
      </div>
    </div>
  );
}
