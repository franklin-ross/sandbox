package hosttool

import "strings"

// BuildInputSchema converts a tool's args into the JSON Schema that describes
// its inputs to the agent (the MCP tool's inputSchema).
//
// This is the single source of truth for the agent-facing schema: sync.go
// precomputes it on the host and ships it into the container, where the MCP
// server returns it verbatim. Keeping it in the same package as
// ValidateAndCoerceArgs lets schema_parity_test.go assert that what the agent
// is told and what the host enforces never drift apart.
//
// Cmd and Validate are deliberately never represented — they run on the host
// and must not reach the sandbox.
func BuildInputSchema(args []Arg) map[string]any {
	properties := make(map[string]any, len(args))
	var required []string

	for _, a := range args {
		t := a.Type
		if t == "" {
			t = "string"
		}
		prop := map[string]any{"type": t}
		if a.Description != "" {
			prop["description"] = a.Description
		}
		if len(a.Enum) > 0 {
			prop["enum"] = a.Enum
		}
		if a.Regex != "" {
			prop["pattern"] = a.Regex
		}
		if a.Min != nil {
			prop["minimum"] = *a.Min
		}
		if a.Max != nil {
			prop["maximum"] = *a.Max
		}
		if a.MinLength != nil {
			prop["minLength"] = *a.MinLength
		}
		if a.MaxLength != nil {
			prop["maxLength"] = *a.MaxLength
		}
		if a.Default != nil {
			prop["default"] = a.Default
		}
		if a.URL != nil {
			// Host allowlists, path prefixes and private-IP blocking can't be
			// expressed as enforceable JSON Schema, so surface them as prose on
			// top of format: uri. The real gate is checkURL, host-side.
			prop["format"] = "uri"
			note := urlConstraintNote(a.URL)
			if d, ok := prop["description"].(string); ok && d != "" {
				prop["description"] = d + ". " + note
			} else {
				prop["description"] = note
			}
		}
		properties[a.Name] = prop

		// An arg is required unless explicitly optional or carrying a default —
		// exactly the conditions under which ValidateAndCoerceArgs errors on a
		// missing value.
		if isRequired(a) && a.Default == nil {
			required = append(required, a.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
		// ValidateAndCoerceArgs rejects any arg it didn't declare; advertise
		// that so the agent doesn't waste a turn on a stray property.
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// urlConstraintNote renders a URL constraint as a human-readable hint for the
// schema description.
func urlConstraintNote(u *URLConstraint) string {
	schemes := u.Schemes
	if len(schemes) == 0 {
		schemes = []string{"https"}
	}
	bits := []string{"scheme: " + strings.Join(schemes, "|")}
	if len(u.Hosts) > 0 {
		bits = append(bits, "host: "+strings.Join(u.Hosts, ", "))
	}
	if u.PathPrefix != "" {
		bits = append(bits, "path prefix: "+u.PathPrefix)
	}
	if u.BlockPrivateIPs == nil || *u.BlockPrivateIPs {
		bits = append(bits, "private/loopback IPs blocked")
	}
	return "URL constraints — " + strings.Join(bits, "; ")
}
