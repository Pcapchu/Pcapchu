import { useState, useEffect } from "react";
import { Trash2, Upload, Loader2, HardDrive } from "lucide-react";
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
import { listPcapFiles, uploadPcap, deletePcapFile } from "@/lib/api";
import type { PcapFile } from "@/lib/types";

interface PcapManagerProps {
  open: boolean;
  onClose: () => void;
  onSelectPcap?: (pcap: PcapFile) => void;
}

export function PcapManager({ open, onClose, onSelectPcap }: PcapManagerProps) {
  const [files, setFiles] = useState<PcapFile[]>([]);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);

  useEffect(() => {
    if (open) {
      setLoading(true);
      listPcapFiles()
        .then((data) => setFiles(data))
        .finally(() => setLoading(false));
    }
  }, [open]);

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploading(true);
    try {
      const pcap = await uploadPcap(file);
      setFiles((prev) => {
        if (prev.some((p) => p.id === pcap.id)) return prev;
        return [pcap, ...prev];
      });
    } finally {
      setUploading(false);
      e.target.value = "";
    }
  };

  const handleDelete = async (id: number) => {
    await deletePcapFile(id);
    setFiles((prev) => prev.filter((f) => f.id !== id));
  };

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <HardDrive className="h-4 w-4" />
            Pcap Management
          </DialogTitle>
          <DialogDescription>
            Upload, manage, and reuse stored pcap files.
          </DialogDescription>
        </DialogHeader>

        <Separator />

        {/* Upload */}
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="relative" disabled={uploading}>
            {uploading ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Upload className="h-4 w-4" />
            )}
            Upload Pcap
            <input
              type="file"
              accept=".pcap,.pcapng,.cap"
              className="absolute inset-0 cursor-pointer opacity-0"
              onChange={handleUpload}
              disabled={uploading}
            />
          </Button>
        </div>

        {/* File list */}
        <ScrollArea className="max-h-[300px]">
          {loading ? (
            <div className="flex justify-center py-8">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : files.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              No pcap files stored yet.
            </p>
          ) : (
            <div className="space-y-1">
              {files.map((f) => (
                <div
                  key={f.id}
                  className="group flex items-center gap-3 rounded-md px-2 py-2 hover:bg-muted/50"
                >
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium truncate">{f.filename}</p>
                    <p className="text-xs text-muted-foreground">
                      {(f.size / 1024 / 1024).toFixed(2)} MB · {new Date(f.created_at).toLocaleDateString()}
                    </p>
                  </div>
                  {onSelectPcap && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-xs"
                      onClick={() => {
                        onSelectPcap(f);
                        onClose();
                      }}
                    >
                      Use
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 opacity-0 group-hover:opacity-100"
                    onClick={() => handleDelete(f.id)}
                  >
                    <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                  </Button>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </DialogContent>
    </Dialog>
  );
}
