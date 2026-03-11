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
		if len(cfg.HostCommands) > 0 {
			port := cfg.EffectiveHostcmdPort()
			if err := cmd.EnsureHostcmdDaemon(port); err != nil {
				return fmt.Errorf("hostcmd daemon: %w", err)
			}
			sessionID := cmd.GenerateSessionID()
			if err := cmd.RegisterHostcmdSession(port, sessionID, cfg.HostCommands, sandboxRoot); err != nil {
				return fmt.Errorf("register hostcmd session: %w", err)
			}
			defer cmd.UnregisterHostcmdSession(port, sessionID)

			extraEnv = map[string]string{
				"SANDBOX_SESSION":      sessionID,
				"SANDBOX_HOSTCMD_PORT": fmt.Sprintf("%d", port),
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
