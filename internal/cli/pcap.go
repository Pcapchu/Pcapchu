package cli

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var pcapCmd = &cobra.Command{
	Use:   "pcap",
	Short: "Manage stored pcap files",
}

func init() {
	rootCmd.AddCommand(pcapCmd)
	pcapCmd.AddCommand(pcapListCmd)
	pcapCmd.AddCommand(pcapDeleteCmd)
}

var pcapListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pcap files stored in the database",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()

		items, err := store.ListPcapFiles(cmd.Context())
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No pcap files stored.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tFILENAME\tSIZE\tSHA256\tCREATED")
		for _, p := range items {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
				p.ID,
				p.Filename,
				humanSize(p.Size),
				p.SHA256[:12]+"...",
				p.CreatedAt.Format("2006-01-02 15:04"),
			)
		}
		w.Flush()
		return nil
	},
}

var pcapDeleteCmd = &cobra.Command{
	Use:   "delete <pcap-id>",
	Short: "Delete a stored pcap file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid pcap ID: %w", err)
		}

		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()

		if err := store.DeletePcapFile(cmd.Context(), id); err != nil {
			return err
		}
		fmt.Printf("Pcap file %d deleted. Sessions referencing it will lose their pcap link.\n", id)
		return nil
	},
}

func humanSize(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
