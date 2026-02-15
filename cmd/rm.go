package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rmAll bool

var rmCmd = &cobra.Command{
	Use:   "rm [path]",
	Short: "Remove a sandbox container",
	Long:  `Remove a sandbox container. The credentials volume is preserved. Use -all to also remove it.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)

		name := containerName(wsPath)
		if containerExists(name) {
			if err := dockerRun("rm", "-f", name); err != nil {
				return fmt.Errorf("remove container: %w", err)
			}
			fmt.Printf("Sandbox %s removed\n", name)
		} else {
			fmt.Printf("No sandbox found for %s\n", wsPath)
		}

		if rmAll {
			if err := dockerRun("volume", "rm", credsVol); err != nil {
				return fmt.Errorf("remove credentials volume: %w", err)
			}
			fmt.Println("Credentials volume removed")
		}

		return nil
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&rmAll, "all", "a", false, "Also remove the credentials volume")
	rootCmd.AddCommand(rmCmd)
}
