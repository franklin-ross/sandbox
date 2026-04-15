package cmd

import (
	"strings"
	"testing"
)

func ptrBool(b bool) *bool        { return &b }
func ptrFloat(f float64) *float64 { return &f }
func ptrInt(i int) *int           { return &i }

func TestValidateArgs_Defaults(t *testing.T) {
	ht := HostTool{
		Args: []HostToolArg{
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
	ht := HostTool{Args: []HostToolArg{{Name: "tag"}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatalf("want required error mentioning tag, got %v", err)
	}
}

func TestValidateArgs_OptionalMissing(t *testing.T) {
	ht := HostTool{Args: []HostToolArg{{Name: "tag", Required: ptrBool(false)}}}
	out, err := ValidateAndCoerceArgs(ht, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, ok := out["tag"]; ok {
		t.Errorf("optional missing arg should not appear in output")
	}
}

func TestValidateArgs_TypeCoerce(t *testing.T) {
	ht := HostTool{Args: []HostToolArg{
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
	ht := HostTool{Args: []HostToolArg{{Name: "n", Type: "integer"}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"n": 1.5})
	if err == nil {
		t.Fatal("want error for non-integer value")
	}
}

func TestValidateArgs_Enum(t *testing.T) {
	ht := HostTool{Args: []HostToolArg{{Name: "e", Enum: []string{"a", "b"}}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"e": "a"}); err != nil {
		t.Errorf("enum ok case: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"e": "c"}); err == nil {
		t.Errorf("enum rejection: want error")
	}
}

func TestValidateArgs_Regex(t *testing.T) {
	ht := HostTool{Args: []HostToolArg{{Name: "v", Regex: `^v\d+$`}}}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"v": "v1"}); err != nil {
		t.Errorf("regex ok: %v", err)
	}
	if _, err := ValidateAndCoerceArgs(ht, map[string]any{"v": "bad"}); err == nil {
		t.Errorf("regex rejection: want error")
	}
}

func TestValidateArgs_MinMax(t *testing.T) {
	ht := HostTool{Args: []HostToolArg{
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
	ht := HostTool{Args: []HostToolArg{
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

func TestValidateArgs_UnknownArg(t *testing.T) {
	ht := HostTool{Args: []HostToolArg{{Name: "a"}}}
	_, err := ValidateAndCoerceArgs(ht, map[string]any{"a": "x", "b": "y"})
	if err == nil || !strings.Contains(err.Error(), "b") {
		t.Fatalf("want unknown-arg error for b, got %v", err)
	}
}
