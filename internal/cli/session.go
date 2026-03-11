package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage investigation sessions",
}

func init() {
	rootCmd.AddCommand(sessionCmd)
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionResumeCmd)
	sessionCmd.AddCommand(sessionDeleteCmd)

	sessionResumeCmd.Flags().IntVar(&sessionResumeRounds, "rounds", 1, "number of additional rounds")
	sessionResumeCmd.Flags().StringVar(&sessionResumeQuery, "query", "", "override the original query")
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()

		items, err := store.ListSessions(cmd.Context())
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No sessions found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tQUERY\tROUNDS\tPCAP\tCREATED\tUPDATED")
		for _, s := range items {
			q := s.SessionTitle
			if len(q) > 60 {
				q = q[:57] + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
				s.ID,
				q,
				s.RoundCount,
				s.PcapSource(),
				s.CreatedAt.Format("2006-01-02 15:04"),
				s.UpdatedAt.Format("2006-01-02 15:04"),
			)
		}
		w.Flush()
		return nil
	},
}

var (
	sessionResumeRounds int
	sessionResumeQuery  string
)

var sessionResumeCmd = &cobra.Command{
	Use:   "resume <session-id>",
	Short: "Resume an existing session and run more rounds",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		return resumeSession(store, args[0], sessionResumeQuery, sessionResumeRounds)
	},
}

var sessionDeleteCmd = &cobra.Command{
	Use:   "delete <session-id>",
	Short: "Delete a session and all its rounds",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()

		if err := store.DeleteSession(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Session %s deleted.\n", args[0])
		return nil
	},
}
