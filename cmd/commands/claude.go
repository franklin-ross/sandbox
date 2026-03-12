package commands

import (
	"fmt"
	"strings"

	cmd "github.com/franklin-ross/sandbox/cmd"
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
	RunE: func(c *cobra.Command, args []string) error {
		for _, a := range args {
			if a == "-h" || a == "--help" {
				return c.Help()
			}
		}

		wsPath, claudeArgs := parseClaudeArgs(args)
		sandboxRoot, workDir := cmd.ResolveWorkspace(wsPath)

		name, err := cmd.EnsureRunning(sandboxRoot)
		if err != nil {
			return err
		}

		cfg, err := cmd.LoadConfig(sandboxRoot)
		if err != nil {
			return err
		}

		var extraEnv map[string]string
		if len(cfg.HostTools) > 0 {
			port := cfg.EffectiveHostToolPort()
			if err := cmd.EnsureHostToolDaemon(port); err != nil {
				return fmt.Errorf("host tool daemon: %w", err)
			}
			sessionID := cmd.GenerateSessionID()
			if err := cmd.RegisterHostToolSession(port, sessionID, cfg.HostTools, sandboxRoot); err != nil {
				return fmt.Errorf("register host tool session: %w", err)
			}
			defer cmd.UnregisterHostToolSession(port, sessionID)

			extraEnv = map[string]string{
				"SANDBOX_SESSION":       sessionID,
				"SANDBOX_HOSTTOOL_PORT": fmt.Sprintf("%d", port),
			}
		}

		execArgs := []string{"claude", "--dangerously-skip-permissions"}
		execArgs = append(execArgs, claudeArgs...)
		return cmd.DockerExec(name, workDir, cfg, extraEnv, execArgs...)
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

	return cmd.ResolvePath(wsPath), claudeArgs
}

func init() {
	cmd.RootCmd.AddCommand(claudeCmd)
}
