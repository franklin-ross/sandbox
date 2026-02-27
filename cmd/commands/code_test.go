package commands

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestVSCodeRemoteURI(t *testing.T) {
	wsPath := "/home/user/projects/myapp"
	tests := []struct {
		containerID string
	}{
		{"abc123def456"},
		{"sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"},
		{"a1b2c3"},
	}

	for _, tt := range tests {
		t.Run(tt.containerID, func(t *testing.T) {
			hexID := hex.EncodeToString([]byte(tt.containerID))
			uri := fmt.Sprintf("vscode-remote://attached-container+%s%s", hexID, wsPath)

			if !strings.HasPrefix(uri, "vscode-remote://attached-container+") {
				t.Errorf("URI missing expected prefix: %q", uri)
			}
			if !strings.HasSuffix(uri, wsPath) {
				t.Errorf("URI missing workspace path suffix: %q", uri)
			}

			parts := strings.SplitN(uri, "+", 2)
			hexPart := strings.TrimSuffix(parts[1], wsPath)
			decoded, err := hex.DecodeString(hexPart)
			if err != nil {
				t.Fatalf("hex decode failed: %v", err)
			}
			if string(decoded) != tt.containerID {
				t.Errorf("round-trip failed: got %q, want %q", string(decoded), tt.containerID)
			}
		})
	}
}
