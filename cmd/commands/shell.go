package commands

import (
	"fmt"

	cmd "github.com/franklin-ross/sandbox/cmd"
	"github.com/franklin-ross/sandbox/cmd/hosttool"
	"github.com/spf13/cobra"
)

var shellCmd = &cobra.Command{
	Use:   "shell [path]",
	Short: "Open a zsh shell in the sandbox",
	Long:  `Open an interactive zsh shell in the sandbox. Starts the sandbox if not running.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		wsPath := "."
		if len(args) > 0 {
			wsPath = args[0]
		}
		return runShell(cmd.ResolvePath(wsPath))
	},
}

func runShell(wsPath string) error {
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
		if err := hosttool.EnsureDaemon(port); err != nil {
			return fmt.Errorf("host tool daemon: %w", err)
		}
		sessionID := hosttool.GenerateSessionID()
		token := hosttool.GenerateToken()
		if err := hosttool.RegisterSession(port, sessionID, token, cfg.HostTools, workDir); err != nil {
			return fmt.Errorf("register host tool session: %w", err)
		}
		defer hosttool.UnregisterSession(port, sessionID)

		extraEnv = map[string]string{
			"SANDBOX_SESSION":        sessionID,
			"SANDBOX_HOSTTOOL_PORT":  fmt.Sprintf("%d", port),
			"SANDBOX_HOSTTOOL_TOKEN": token,
		}
	}

	return cmd.DockerExec(name, workDir, cfg, extraEnv, "/bin/zsh")
}

func init() {
	cmd.RootCmd.AddCommand(shellCmd)
}
