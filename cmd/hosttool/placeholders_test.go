package hosttool

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected and returns what was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestURL_WarnsWhenNoAllowlist(t *testing.T) {
	ht := Tool{
		Name: "fetch",
		Cmd:  "curl ${u}",
		Args: []Arg{{Name: "u", URL: &URLConstraint{}}}, // block_private_ips defaults on, no hosts
	}
	out := captureStderr(t, func() {
		if err := ValidatePlaceholders(ht); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})
	if !strings.Contains(out, "no host allowlist") {
		t.Errorf("expected allowlist warning, got %q", out)
	}
}

func TestURL_NoWarnWithAllowlist(t *testing.T) {
	ht := Tool{
		Name: "fetch",
		Cmd:  "curl ${u}",
		Args: []Arg{{Name: "u", URL: &URLConstraint{Hosts: []string{"github.com"}}}},
	}
	out := captureStderr(t, func() {
		if err := ValidatePlaceholders(ht); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})
	if strings.Contains(out, "no host allowlist") {
		t.Errorf("did not expect allowlist warning, got %q", out)
	}
}

func TestURL_NoWarnWhenBlockingDisabled(t *testing.T) {
	// With block_private_ips off the author has explicitly opted out, so the
	// nudge would be noise.
	ht := Tool{
		Name: "fetch",
		Cmd:  "curl ${u}",
		Args: []Arg{{Name: "u", URL: &URLConstraint{BlockPrivateIPs: ptrBool(false)}}},
	}
	out := captureStderr(t, func() {
		if err := ValidatePlaceholders(ht); err != nil {
			t.Fatalf("validate: %v", err)
		}
	})
	if strings.Contains(out, "no host allowlist") {
		t.Errorf("did not expect warning when blocking disabled, got %q", out)
	}
}
