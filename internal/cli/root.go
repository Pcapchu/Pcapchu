package cli

import (
	"os"

	"github.com/Pcapchu/Pcapchu/internal/storage"
	"github.com/spf13/cobra"
)

var dbPath string

var rootCmd = &cobra.Command{
	Use:   "pcapchu",
	Short: "AI-powered network forensics assistant",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "./pcapchu.db", "SQLite database path")
}

// Execute runs the root command. Called from main().
func Execute() {
	// Normalise single-dash long flags (e.g. -help → --help) so that the
	// Go-standard "flag" convention works with cobra/pflag.
	for i, arg := range os.Args {
		if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
			os.Args[i] = "-" + arg
		}
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func openStore() (*storage.Store, error) {
	return storage.New(dbPath)
}
