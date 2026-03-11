import type { SSEEvent } from "@/lib/types";
import { UserQueryMessage } from "./messages/UserQueryMessage";
import { SystemMessage } from "./messages/SystemMessage";
import { PlanMessage } from "./messages/PlanMessage";
import { StepMessage } from "./messages/StepMessage";
import { ReportMessage } from "./messages/ReportMessage";
import { ErrorMessage } from "./messages/ErrorMessage";
import { RoundMessage } from "./messages/RoundMessage";

/** Renders a single SSE event as the appropriate chat message component. */
export function ChatMessage({ event, completedStepIds }: { event: SSEEvent; completedStepIds?: Set<number> }) {
  const completed = completedStepIds ?? new Set<number>();
  switch (event.type) {
    case "session.created":
    case "session.resumed":
      return <UserQueryMessage data={event.data} />;

    case "analysis.completed":
      return <SystemMessage icon="check" text="Investigation completed" variant="success" />;

    case "pcap.loaded":
      return (
        <SystemMessage
          icon="file"
          text={`Pcap loaded: ${event.data.filename} (${formatBytes(Number(event.data.size))})`}
        />
      );

    case "round.started":
      return <RoundMessage type="start" data={event.data} />;

    case "round.completed":
      return <RoundMessage type="end" data={event.data} />;

    case "plan.created":
      return <PlanMessage data={event.data} completedStepIds={completed} />;

    case "step.started":
    case "step.findings":
    case "step.completed":
      return <StepMessage type={event.type} data={event.data} completedStepIds={completed} />;

    case "report.generated":
      return <ReportMessage data={event.data} />;

    case "plan.error":
    case "step.error":
    case "error":
      return <ErrorMessage data={event.data} />;

    case "info":
      return <SystemMessage icon="info" text={String(event.data.message || "")} />;

    default:
      return null;
  }
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / 1024 / 1024).toFixed(1) + " MB";
}
