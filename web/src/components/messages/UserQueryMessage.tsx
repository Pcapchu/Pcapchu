import { User, FileSearch } from "lucide-react";

export function UserQueryMessage({ data }: { data: Record<string, unknown> }) {
  return (
    <div className="flex gap-3 px-4 py-3">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground">
        <User className="h-3.5 w-3.5" />
      </div>
      <div className="flex-1 min-w-0 pt-0.5">
        <p className="text-sm font-medium">{String(data.user_query || "")}</p>
        {data.pcap_source ? (
          <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
            <FileSearch className="h-3 w-3" />
            pcap source: {String(data.pcap_source)}
          </div>
        ) : null}
      </div>
    </div>
  );
}
