package hosttool

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// lookupIP is indirected so tests can replace DNS resolution.
var lookupIP = net.LookupIP

// ValidateAndCoerceArgs validates an input arg map against a Tool's arg
// schema and returns a map of string values ready for shell substitution.
// It applies defaults, coerces types, and enforces enum/regex/min/max/length
// constraints. URL and validate-cmd checks are applied by the caller after
// this function succeeds.
func ValidateAndCoerceArgs(ht Tool, input map[string]any) (map[string]string, error) {
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

func isRequired(a Arg) bool {
	if a.Required == nil {
		return true
	}
	return *a.Required
}

func coerceArg(a Arg, raw any) (string, error) {
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

func checkConstraints(a Arg, str string, raw any) error {
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
	if err := checkURL(a, str); err != nil {
		return err
	}
	return nil
}

func checkURL(a Arg, str string) error {
	if a.URL == nil {
		return nil
	}
	u, err := url.Parse(str)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid URL: missing host")
	}
	if u.User != nil {
		return fmt.Errorf("URL must not contain userinfo")
	}

	schemes := a.URL.Schemes
	if len(schemes) == 0 {
		schemes = []string{"https"}
	}
	if !containsFold(schemes, u.Scheme) {
		return fmt.Errorf("URL scheme %q not in %v", u.Scheme, schemes)
	}

	host := u.Hostname()
	if len(a.URL.Hosts) > 0 && !matchHost(a.URL.Hosts, host) {
		return fmt.Errorf("URL host %q not in allowlist %v", host, a.URL.Hosts)
	}
	if a.URL.PathPrefix != "" && !strings.HasPrefix(u.Path, a.URL.PathPrefix) {
		return fmt.Errorf("URL path %q does not start with %q", u.Path, a.URL.PathPrefix)
	}

	block := true
	if a.URL.BlockPrivateIPs != nil {
		block = *a.URL.BlockPrivateIPs
	}
	if block {
		ips, err := lookupIP(host)
		if err != nil {
			return fmt.Errorf("resolve %q: %w", host, err)
		}
		for _, ip := range ips {
			if isBlockedIP(ip) {
				return fmt.Errorf("URL resolves to blocked (private/loopback/link-local) IP %s", ip)
			}
		}
	}
	return nil
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func matchHost(patterns []string, host string) bool {
	host = strings.ToLower(host)
	for _, p := range patterns {
		ok, _ := filepath.Match(strings.ToLower(p), host)
		if ok {
			return true
		}
	}
	return false
}

var ipv6ULA = mustCIDR("fc00::/7")

func mustCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}

func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	return ipv6ULA.Contains(ip)
}
