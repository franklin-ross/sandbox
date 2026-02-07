package cmd

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var codeCmd = &cobra.Command{
	Use:   "code [path]",
	Short: "Open VSCode attached to the sandbox",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = resolvePath(wsPath)

		name, err := ensureRunning(wsPath)
		if err != nil {
			return err
		}

		// Get container ID for VSCode remote URI
		out, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).Output()
		if err != nil {
			return fmt.Errorf("get container id: %w", err)
		}
		id := strings.TrimSpace(string(out))
		hexID := hex.EncodeToString([]byte(id))
		uri := fmt.Sprintf("vscode-remote://attached-container+%s/workspace", hexID)

		fmt.Printf("Opening VSCode for %s...\n", wsPath)
		c := exec.Command("code", "--folder-uri", uri)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func init() {
	rootCmd.AddCommand(codeCmd)
}
