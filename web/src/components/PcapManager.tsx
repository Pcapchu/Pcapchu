import { useState, useEffect, useCallback, useRef } from "react";
import {
  Trash2,
  Upload,
  Loader2,
  HardDrive,
  FileText,
  AlertTriangle,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { listPcapFiles, uploadPcap, deletePcapFile } from "@/lib/api";
import { useStore } from "@/lib/store";
import type { PcapFile } from "@/lib/types";

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`;
}

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return "just now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(dateStr).toLocaleDateString();
}

export function PcapManager() {
  const open = useStore((s) => s.pcapManagerOpen);
  const onClose = () => useStore.getState().setPcapManagerOpen(false);
  const onAnalyze = useStore((s) => s.handleAnalyzeFromManager);
  const [files, setFiles] = useState<PcapFile[]>([]);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<PcapFile | null>(null);
  const dropRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (open) {
      setLoading(true);
      listPcapFiles()
        .then((data) => setFiles(data))
        .finally(() => setLoading(false));
    }
  }, [open]);

  const doUpload = useCallback(
    async (file: File) => {
      setUploading(true);
      try {
        const res = await uploadPcap(file);
        // Refresh list
        const updated = await listPcapFiles();
        setFiles(updated);
        // If onAnalyze is provided, auto-start analysis on the new session
        if (onAnalyze) {
          onAnalyze(res.session_id);
        }
      } finally {
        setUploading(false);
      }
    },
    [onAnalyze]
  );

  const handleFileInput = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    await doUpload(file);
    e.target.value = "";
  };

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDragging(false);
      const file = e.dataTransfer.files[0];
      if (
        file &&
        (file.name.endsWith(".pcap") ||
          file.name.endsWith(".pcapng") ||
          file.name.endsWith(".cap"))
      ) {
        doUpload(file);
      }
    },
    [doUpload]
  );

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(true);
  };

  const handleDragLeave = (e: React.DragEvent) => {
    // Only leave if truly leaving the drop zone
    if (dropRef.current && !dropRef.current.contains(e.relatedTarget as Node)) {
      setDragging(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    await deletePcapFile(deleteTarget.id);
    setFiles((prev) => prev.filter((f) => f.id !== deleteTarget.id));
    setDeleteTarget(null);
  };

  return (
    <>
      <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <HardDrive className="h-4 w-4" />
              Pcap Management
            </DialogTitle>
            <DialogDescription>
              Upload, manage, and analyze stored pcap files.
            </DialogDescription>
          </DialogHeader>

          <Separator />

          {/* Drop zone */}
          <div
            ref={dropRef}
            onDrop={handleDrop}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            className={`relative flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-6 transition-colors ${
              dragging
                ? "border-primary bg-primary/5"
                : "border-muted-foreground/25 hover:border-muted-foreground/50"
            }`}
          >
            {uploading ? (
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            ) : (
              <Upload className="h-6 w-6 text-muted-foreground" />
            )}
            <div className="text-center">
              <p className="text-sm font-medium">
                {dragging ? "Drop your pcap file here" : "Drag & drop a pcap file"}
              </p>
              <p className="text-xs text-muted-foreground mt-0.5">
                or{" "}
                <label className="cursor-pointer underline underline-offset-2 hover:text-foreground">
                  browse files
                  <input
                    type="file"
                    accept=".pcap,.pcapng,.cap"
                    className="hidden"
                    onChange={handleFileInput}
                    disabled={uploading}
                  />
                </label>
              </p>
            </div>
          </div>

          {/* File list */}
          <ScrollArea className="max-h-[320px]">
            {loading ? (
              <div className="flex justify-center py-8">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : files.length === 0 ? (
              <div className="flex flex-col items-center gap-2 py-8 text-muted-foreground">
                <FileText className="h-8 w-8" />
                <p className="text-sm">No pcap files stored yet.</p>
                <p className="text-xs">
                  Upload a file above to get started.
                </p>
              </div>
            ) : (
              <div className="space-y-1">
                {files.map((f) => (
                  <div
                    key={f.id}
                    className="group flex items-center gap-3 rounded-md px-3 py-2.5 hover:bg-muted/50 transition-colors"
                  >
                    <FileText className="h-4 w-4 shrink-0 text-muted-foreground" />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium truncate">
                        {f.filename}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {formatBytes(f.size)}
                        {f.sha256 && (
                          <>
                            {" · "}
                            <span className="font-mono">
                              {f.sha256.slice(0, 12)}...
                            </span>
                          </>
                        )}
                        {" · "}
                        {timeAgo(f.created_at)}
                      </p>
                    </div>
                    <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-muted-foreground hover:text-destructive"
                        title="Delete"
                        onClick={() => setDeleteTarget(f)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </ScrollArea>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-destructive" />
              Delete pcap file?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently remove{" "}
              <span className="font-medium">{deleteTarget?.filename}</span> from
              storage. Sessions that used this file will lose the ability to
              re-run.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmDelete}
              className="bg-destructive text-white hover:bg-destructive/90"
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
