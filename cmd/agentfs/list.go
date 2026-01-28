package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stores",
	Long:  `List all sparse bundle stores.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		stores, err := storeManager.List()
		if err != nil {
			exitWithError(ExitError, "%v", err)
		}

		if jsonFlag {
			type storeJSON struct {
				Name        string `json:"name"`
				Size        string `json:"size"`
				SizeBytes   int64  `json:"size_bytes"`
				Mounted     bool   `json:"mounted"`
				MountPath   string `json:"mount_path"`
				Checkpoints int    `json:"checkpoints"`
			}

			var output []storeJSON
			for _, s := range stores {
				count, _ := cpManager.Count(s.Name)
				output = append(output, storeJSON{
					Name:        s.Name,
					Size:        humanize.IBytes(uint64(s.SizeBytes)),
					SizeBytes:   s.SizeBytes,
					Mounted:     s.MountedAt != nil,
					MountPath:   s.MountPath,
					Checkpoints: count,
				})
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(output)
			return
		}

		if len(stores) == 0 {
			fmt.Println("No stores found. Use 'agentfs init <name>' to create one.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSIZE\tMOUNTED\tCHECKPOINTS")

		for _, s := range stores {
			mounted := "No"
			if s.MountedAt != nil {
				mounted = "Yes"
			}

			count, _ := cpManager.Count(s.Name)

			fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
				s.Name,
				humanize.IBytes(uint64(s.SizeBytes)),
				mounted,
				count,
			)
		}
		w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
