package hosttool

import (
	"net"
	"strings"
	"testing"
)

func ptrBool(b bool) *bool        { return &b }
func ptrFloat(f float64) *float64 { return &f }
func ptrInt(i int) *int           { return &i }

func TestValidateArgs_Defaults(t *testing.T) {
	ht := Tool{
		Args: []Arg{
			{Name: "env", Default: "staging"},
		},
	}
	out, err := ValidateAndCoerceArgs(ht, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["env"] != "staging" {
		t.Errorf("env = %q, want staging", out["env"])
	}
}

func TestValidateArgs_RequiredMissing(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "tag"}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatalf("want required error mentioning tag, got %v", err)
	}
}

func TestValidateArgs_OptionalMissing(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "tag", Required: ptrBool(false)}}}
	out, err := ValidateAndCoerceArgs(ht, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, ok := out["tag"]; ok {
		t.Errorf("optional missing arg should not appear in output")
	}
}

func TestValidateArgs_TypeCoerce(t *testing.T) {
	ht := Tool{Args: []Arg{
		{Name: "n", Type: "integer"},
		{Name: "f", Type: "number"},
		{Name: "b", Type: "boolean"},
	}}
	out, err := ValidateAndCoerceArgs(ht, map[string]any{
		"n": float64(5), "f": float64(1.5), "b": true,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if out["n"] != "5" || out["f"] != "1.5" || out["b"] != "true" {
		t.Errorf("got %+v", out)
	}
}

func TestValidateArgs_IntegerRejectsFloat(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "n", Type: "integer"}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"n": 1.5})
	if err == nil {
		t.Fatal("want error for non-integer value")
	}
}

func TestValidateArgs_Enum(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "e", Enum: []string{"a", "b"}}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"e": "a"}); err != nil {
		t.Errorf("enum ok case: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"e": "c"}); err == nil {
		t.Errorf("enum rejection: want error")
	}
}

func TestValidateArgs_Regex(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "v", Regex: `^v\d+$`}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"v": "v1"}); err != nil {
		t.Errorf("regex ok: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"v": "bad"}); err == nil {
		t.Errorf("regex rejection: want error")
	}
}

func TestValidateArgs_MinMax(t *testing.T) {
	ht := Tool{Args: []Arg{
		{Name: "n", Type: "integer", Min: ptrFloat(0), Max: ptrFloat(10)},
	}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"n": float64(5)}); err != nil {
		t.Errorf("mid ok: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"n": float64(-1)}); err == nil {
		t.Errorf("below min: want error")
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"n": float64(11)}); err == nil {
		t.Errorf("above max: want error")
	}
}

func TestValidateArgs_Length(t *testing.T) {
	ht := Tool{Args: []Arg{
		{Name: "s", MinLength: ptrInt(2), MaxLength: ptrInt(4)},
	}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"s": "ab"}); err != nil {
		t.Errorf("min edge: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"s": "a"}); err == nil {
		t.Error("too short: want error")
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"s": "abcde"}); err == nil {
		t.Error("too long: want error")
	}
}

func TestURL_DefaultBlocksPrivate(t *testing.T) {
	old := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.1")}, nil
	}
	defer func() { lookupIP = old }()

	ht := Tool{Args: []Arg{{Name: "u", URL: &URLConstraint{}}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "https://evil.example/"})
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("want private-ip error, got %v", err)
	}
}

func TestURL_SchemeAllowlist(t *testing.T) {
	old := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	defer func() { lookupIP = old }()

	ht := Tool{Args: []Arg{{Name: "u", URL: &URLConstraint{}}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "http://example.com/"})
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("want scheme rejection (default https), got %v", err)
	}
}

func TestURL_HostGlob(t *testing.T) {
	old := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	defer func() { lookupIP = old }()

	ht := Tool{Args: []Arg{{Name: "u", URL: &URLConstraint{
		Hosts: []string{"*.github.io", "github.com"},
	}}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "https://foo.github.io/x"}); err != nil {
		t.Errorf("glob match: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "https://gitlab.com/x"}); err == nil {
		t.Error("non-match: want error")
	}
}

func TestURL_PathPrefix(t *testing.T) {
	old := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	}
	defer func() { lookupIP = old }()

	ht := Tool{Args: []Arg{{Name: "u", URL: &URLConstraint{PathPrefix: "/api/"}}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "https://x.com/api/foo"}); err != nil {
		t.Errorf("prefix match: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "https://x.com/other"}); err == nil {
		t.Error("prefix miss: want error")
	}
}

func TestURL_AllowPrivateWhenDisabled(t *testing.T) {
	old := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	defer func() { lookupIP = old }()

	ht := Tool{Args: []Arg{{Name: "u", URL: &URLConstraint{
		BlockPrivateIPs: ptrBool(false),
	}}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "https://localhost/"}); err != nil {
		t.Errorf("allow-private: %v", err)
	}
}

func TestURL_MetadataIP(t *testing.T) {
	old := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	defer func() { lookupIP = old }()

	ht := Tool{Args: []Arg{{Name: "u", URL: &URLConstraint{}}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"u": "https://metadata.example/"})
	if err == nil {
		t.Fatal("want rejection of link-local metadata IP")
	}
}

func TestValidateCmd_Accept(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "s", Validate: "grep -q ok"}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"s": "ok"}); err != nil {
		t.Errorf("accept case: %v", err)
	}
}

func TestValidateCmd_Reject(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "s", Validate: "grep -q ok"}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"s": "nope"})
	if err == nil || !strings.Contains(err.Error(), "validate") {
		t.Fatalf("reject case: want validate error, got %v", err)
	}
}

func TestValidateArgs_UnknownArg(t *testing.T) {
	ht := Tool{Args: []Arg{{Name: "a"}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"a": "x", "b": "y"})
	if err == nil || !strings.Contains(err.Error(), "b") {
		t.Fatalf("want unknown-arg error for b, got %v", err)
	}
}
