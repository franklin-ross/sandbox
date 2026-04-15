package cmd

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// ValidateAndCoerceArgs validates an input arg map against a HostTool's arg
// schema and returns a map of string values ready for shell substitution.
// It applies defaults, coerces types, and enforces enum/regex/min/max/length
// constraints. URL and validate-cmd checks are applied by the caller after
// this function succeeds.
func ValidateAndCoerceArgs(ht HostTool, input map[string]any) (map[string]string, error) {
	declared := make(map[string]bool, len(ht.Args))
	for _, a := range ht.Args {
		declared[a.Name] = true
	}
	for name := range input {
		if !declared[name] {
			return nil, fmt.Errorf("unknown arg %q", name)
		}
	}

	out := make(map[string]string, len(ht.Args))
	for _, a := range ht.Args {
		raw, present := input[a.Name]
		if !present {
			if a.Default != nil {
				raw = a.Default
				present = true
			} else if isRequired(a) {
				return nil, fmt.Errorf("arg %q: required", a.Name)
			} else {
				continue
			}
		}
		_ = present

		str, err := coerceArg(a, raw)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", a.Name, err)
		}
		if err := checkConstraints(a, str, raw); err != nil {
			return nil, fmt.Errorf("arg %q: %w", a.Name, err)
		}
		out[a.Name] = str
	}
	return out, nil
}

func isRequired(a HostToolArg) bool {
	if a.Required == nil {
		return true
	}
	return *a.Required
}

func coerceArg(a HostToolArg, raw any) (string, error) {
	t := a.Type
	if t == "" {
		t = "string"
	}
	switch t {
	case "string":
		s, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("expected string, got %T", raw)
		}
		return s, nil
	case "integer":
		f, ok := toFloat(raw)
		if !ok {
			return "", fmt.Errorf("expected integer, got %T", raw)
		}
		if f != math.Trunc(f) {
			return "", fmt.Errorf("expected integer, got %v", raw)
		}
		return strconv.FormatInt(int64(f), 10), nil
	case "number":
		f, ok := toFloat(raw)
		if !ok {
			return "", fmt.Errorf("expected number, got %T", raw)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case "boolean":
		b, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("expected boolean, got %T", raw)
		}
		return strconv.FormatBool(b), nil
	default:
		return "", fmt.Errorf("unknown type %q", t)
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	}
	return 0, false
}

func checkConstraints(a HostToolArg, str string, raw any) error {
	if len(a.Enum) > 0 {
		ok := false
		for _, e := range a.Enum {
			if str == e {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("not in enum %v", a.Enum)
		}
	}
	if a.Regex != "" {
		re, err := regexp.Compile(a.Regex)
		if err != nil {
			return fmt.Errorf("invalid regex %q: %w", a.Regex, err)
		}
		if !re.MatchString(str) {
			return fmt.Errorf("does not match %q", a.Regex)
		}
	}
	if a.Min != nil || a.Max != nil {
		f, ok := toFloat(raw)
		if !ok {
			return fmt.Errorf("min/max require numeric type")
		}
		if a.Min != nil && f < *a.Min {
			return fmt.Errorf("%v < min %v", f, *a.Min)
		}
		if a.Max != nil && f > *a.Max {
			return fmt.Errorf("%v > max %v", f, *a.Max)
		}
	}
	if a.MinLength != nil && len(str) < *a.MinLength {
		return fmt.Errorf("length %d < min_length %d", len(str), *a.MinLength)
	}
	if a.MaxLength != nil && len(str) > *a.MaxLength {
		return fmt.Errorf("length %d > max_length %d", len(str), *a.MaxLength)
	}
	return nil
}
