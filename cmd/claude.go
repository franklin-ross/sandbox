package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

var claudeCmd = &cobra.Command{
	Use:   "claude [path] [-- claude-args...]",
	Short: "Open Claude Code in the sandbox",
	Long: `Open an interactive Claude Code session with --dangerously-skip-permissions.
Pass extra arguments to Claude after --.

Examples:
  sandbox claude
  sandbox claude ~/proj
  sandbox claude . -- -p "fix the tests"`,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Handle help manually since we disabled flag parsing
		for _, a := range args {
			if a == "-h" || a == "--help" {
				return cmd.Help()
			}
		}

		wsPath, claudeArgs := parseClaudeArgs(args)
		sandboxRoot, workDir := resolveWorkspace(wsPath)

		name, err := ensureRunning(sandboxRoot)
		if err != nil {
			return err
		}

		cfg, err := loadConfig(sandboxRoot)
		if err != nil {
			return err
		}
		execArgs := []string{"claude", "--dangerously-skip-permissions"}
		execArgs = append(execArgs, claudeArgs...)
		return dockerExec(name, workDir, cfg, execArgs...)
	},
}

// parseClaudeArgs splits args into a workspace path and extra claude flags.
// Everything after "--" is passed to claude. The first positional arg before
// "--" (if it doesn't start with "-") is treated as the workspace path.
func parseClaudeArgs(args []string) (string, []string) {
	var positional []string
	var claudeArgs []string
	pastSep := false

	for _, a := range args {
		if a == "--" {
			pastSep = true
			continue
		}
		if pastSep {
			claudeArgs = append(claudeArgs, a)
		} else {
			positional = append(positional, a)
		}
	}

	wsPath := "."
	if len(positional) > 0 && !strings.HasPrefix(positional[0], "-") {
		wsPath = positional[0]
	}

	return resolvePath(wsPath), claudeArgs
}

func init() {
	rootCmd.AddCommand(claudeCmd)
}
