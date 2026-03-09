import {
  Info,
  CheckCircle2,
  Play,
  Activity,
  FileText,
} from "lucide-react";
import { cn } from "@/lib/utils";

const icons = {
  info: Info,
  check: CheckCircle2,
  play: Play,
  activity: Activity,
  file: FileText,
} as const;

interface SystemMessageProps {
  icon: keyof typeof icons;
  text: string;
  variant?: "default" | "success";
}

export function SystemMessage({ icon, text, variant = "default" }: SystemMessageProps) {
  const Icon = icons[icon];
  return (
    <div className="flex justify-center px-4 py-1.5">
      <div
        className={cn(
          "flex items-center gap-1.5 rounded-full px-3 py-1 text-xs",
          variant === "success"
            ? "bg-green-50 text-green-700 dark:bg-green-950 dark:text-green-400"
            : "bg-muted text-muted-foreground"
        )}
      >
        <Icon className="h-3 w-3" />
        {text}
      </div>
    </div>
  );
}
