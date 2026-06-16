package hosttool

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// These tests pin the agent-facing JSON Schema (BuildInputSchema) to what the
// host actually enforces (ValidateAndCoerceArgs). BuildInputSchema is the
// single source of truth shipped into the sandbox — the MCP server returns it
// verbatim — so if a validator rule changes meaning, the schema must move with
// it or the agent is told a constraint that no longer holds. Each case names
// the validator behaviour it mirrors.

func schemaProps(t *testing.T, args []Arg) (map[string]any, map[string]any) {
	t.Helper()
	schema := BuildInputSchema(args)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object: %#v", schema)
	}
	return props, schema
}

func schemaProp(t *testing.T, args []Arg, name string) map[string]any {
	t.Helper()
	props, _ := schemaProps(t, args)
	p, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("no property %q in %#v", name, props)
	}
	return p
}

// Mirrors TestValidateArgs_UnknownArg: the validator rejects undeclared args,
// so the schema must say additionalProperties:false.
func TestSchemaParity_RejectsUnknownArgs(t *testing.T) {
	_, schema := schemaProps(t, []Arg{{Name: "a"}})
	if schema["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
}

// Mirrors TestValidateArgs_Required/Optional/Defaults: an arg errors when
// missing only if it is required and has no default.
func TestSchemaParity_Required(t *testing.T) {
	args := []Arg{
		{Name: "req"},                           // required by default
		{Name: "opt", Required: ptrBool(false)}, // optional
		{Name: "def", Default: "staging"},       // defaulted → not required
	}
	_, schema := schemaProps(t, args)
	req, _ := schema["required"].([]string)
	if !slices.Contains(req, "req") {
		t.Errorf("required missing 'req': %v", req)
	}
	if slices.Contains(req, "opt") || slices.Contains(req, "def") {
		t.Errorf("required must exclude optional/defaulted args: %v", req)
	}

	// The defaulted value the validator would inject must be visible to the agent.
	if p := schemaProp(t, args, "def"); p["default"] != "staging" {
		t.Errorf("default = %v, want staging", p["default"])
	}
}

// Mirrors coerceArg: absent type defaults to string on both sides.
func TestSchemaParity_TypeDefaultsToString(t *testing.T) {
	if p := schemaProp(t, []Arg{{Name: "s"}}, "s"); p["type"] != "string" {
		t.Errorf("type = %v, want string", p["type"])
	}
}

// Mirrors checkConstraints: every scalar constraint the validator enforces has
// a matching JSON Schema keyword.
func TestSchemaParity_ScalarConstraints(t *testing.T) {
	args := []Arg{{
		Name:      "n",
		Type:      "integer",
		Min:       ptrFloat(0),
		Max:       ptrFloat(10),
		Regex:     `^\d+$`,
		MinLength: ptrInt(1),
		MaxLength: ptrInt(3),
		Enum:      []string{"0", "5"},
	}}
	p := schemaProp(t, args, "n")

	want := map[string]any{
		"type":      "integer",
		"minimum":   float64(0),
		"maximum":   float64(10),
		"pattern":   `^\d+$`,
		"minLength": 1,
		"maxLength": 3,
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %v (%T), want %v (%T)", k, p[k], p[k], v, v)
		}
	}
	if enum, ok := p["enum"].([]string); !ok || !slices.Equal(enum, []string{"0", "5"}) {
		t.Errorf("enum = %v, want [0 5]", p["enum"])
	}
}

// Mirrors checkURL: URL constraints aren't expressible as enforceable JSON
// Schema, so they must at least be described (format + a prose note) so the
// agent knows the host-side gate exists.
func TestSchemaParity_URLDescribed(t *testing.T) {
	p := schemaProp(t, []Arg{{Name: "u", URL: &URLConstraint{
		Hosts:      []string{"api.github.com"},
		PathPrefix: "/repos",
	}}}, "u")

	if p["format"] != "uri" {
		t.Errorf("format = %v, want uri", p["format"])
	}
	desc, _ := p["description"].(string)
	for _, want := range []string{"scheme: https", "api.github.com", "/repos", "private/loopback IPs blocked"} {
		if !strings.Contains(desc, want) {
			t.Errorf("URL description %q missing %q", desc, want)
		}
	}
}

// A user-provided description must survive when the URL note is appended.
func TestSchemaParity_URLNoteKeepsDescription(t *testing.T) {
	p := schemaProp(t, []Arg{{Name: "u", Description: "the target", URL: &URLConstraint{}}}, "u")
	if desc, _ := p["description"].(string); !strings.HasPrefix(desc, "the target. ") {
		t.Errorf("description %q should keep the original text", desc)
	}
}

// Security parity: Cmd and Validate run on the host and must never appear in
// the schema shipped into the sandbox.
func TestSchemaParity_NeverLeaksHostOnlyFields(t *testing.T) {
	args := []Arg{{Name: "s", Validate: "secret-host-check --token AABBCCDD"}}
	blob, err := json.Marshal(BuildInputSchema(args))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"secret-host-check", "AABBCCDD", "validate", "cmd"} {
		if strings.Contains(string(blob), leak) {
			t.Errorf("schema leaked host-only data %q: %s", leak, blob)
		}
	}
}
