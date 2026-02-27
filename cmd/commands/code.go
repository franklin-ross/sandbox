package commands

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/spf13/cobra"
)

var codeCmd = &cobra.Command{
	Use:   "code [path]",
	Short: "Open VSCode attached to the sandbox",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		wsPath = cmd.ResolvePath(wsPath)
		sandboxRoot, workDir := cmd.ResolveWorkspace(wsPath)

		name, err := cmd.EnsureRunning(sandboxRoot)
		if err != nil {
			return err
		}

		out, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).Output()
		if err != nil {
			return fmt.Errorf("get container id: %w", err)
		}
		id := strings.TrimSpace(string(out))
		hexID := hex.EncodeToString([]byte(id))
		uri := fmt.Sprintf("vscode-remote://attached-container+%s%s", hexID, workDir)

		fmt.Printf("Opening VSCode for %s...\n", workDir)
		c := exec.Command("code", "--folder-uri", uri)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func init() {
	cmd.RootCmd.AddCommand(codeCmd)
}
