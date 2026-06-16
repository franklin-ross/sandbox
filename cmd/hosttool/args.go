package hosttool

import (
	"context"
	"fmt"
	"math"
	"net"
	"maps"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
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
		pin, err := checkURL(a, str)
		if err != nil {
			return nil, fmt.Errorf("arg %q: %w", a.Name, err)
		}
		if a.Validate != "" {
			if err := runValidateCmd(a.Validate, str); err != nil {
				return nil, fmt.Errorf("arg %q: %w", a.Name, err)
			}
		}
		out[a.Name] = str
		// Expose the validated address so a command can force its HTTP client to
		// connect to the exact IP we checked (e.g. `curl --resolve ${u_resolve}
		// ${u}`), closing the DNS-rebinding gap between validation and the
		// command's own re-resolution. Components are provided for clients that
		// pin differently than curl's --resolve.
		if pin != nil {
			maps.Copy(out, pin.values(a.Name))
		}
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
	return nil
}

func runValidateCmd(cmdStr, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	c.Stdin = strings.NewReader(value)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("validate cmd rejected: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// urlDerivedSuffixes are the placeholder suffixes a URL arg named <name>
// exposes once its host resolves to a validated IP:
//
//	${<name>_resolve}  -> "host:port:ip"  (curl/wget2 --resolve form)
//	${<name>_ip}       -> the validated IP
//	${<name>_host}     -> the hostname
//	${<name>_port}     -> the port
//
// The components let clients that pin differently than curl assemble their own
// form (a Host header plus IP, an http:// URL rewrite, etc.).
var urlDerivedSuffixes = []string{"_resolve", "_ip", "_host", "_port"}

// urlPin holds the validated address of a URL arg.
type urlPin struct {
	host, port, ip string
}

// values returns the derived placeholder name→value map for an arg named name.
func (p urlPin) values(name string) map[string]string {
	return map[string]string{
		name + "_resolve": p.host + ":" + p.port + ":" + p.ip,
		name + "_ip":      p.ip,
		name + "_host":    p.host,
		name + "_port":    p.port,
	}
}

// checkURL validates a URL-typed arg. When private-IP blocking is enabled it
// resolves the host and rejects any blocked address, then returns a urlPin with
// the validated address. A command can feed the pin to its HTTP client (e.g.
// `curl --resolve ${arg_resolve} ${arg}`) so the fetch connects to exactly the
// address we checked — closing the DNS-rebinding / TOCTOU gap where the
// command's own DNS lookup could resolve differently. Returns (nil, nil) when
// there is no URL constraint, blocking is disabled, or the port is unknown.
func checkURL(a Arg, str string) (*urlPin, error) {
	if a.URL == nil {
		return nil, nil
	}
	u, err := url.Parse(str)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid URL: missing host")
	}
	if u.User != nil {
		return nil, fmt.Errorf("URL must not contain userinfo")
	}

	schemes := a.URL.Schemes
	if len(schemes) == 0 {
		schemes = []string{"https"}
	}
	if !containsFold(schemes, u.Scheme) {
		return nil, fmt.Errorf("URL scheme %q not in %v", u.Scheme, schemes)
	}

	host := u.Hostname()
	if len(a.URL.Hosts) > 0 && !matchHost(a.URL.Hosts, host) {
		return nil, fmt.Errorf("URL host %q not in allowlist %v", host, a.URL.Hosts)
	}
	if a.URL.PathPrefix != "" && !strings.HasPrefix(u.Path, a.URL.PathPrefix) {
		return nil, fmt.Errorf("URL path %q does not start with %q", u.Path, a.URL.PathPrefix)
	}

	block := true
	if a.URL.BlockPrivateIPs != nil {
		block = *a.URL.BlockPrivateIPs
	}
	if !block {
		return nil, nil
	}

	ips, err := lookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %q: no addresses", host)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, fmt.Errorf("URL resolves to blocked (private/loopback/link-local) IP %s", ip)
		}
	}
	// All returned addresses passed; pin the first.
	port := u.Port()
	if port == "" {
		port = defaultPort(u.Scheme)
	}
	if port == "" {
		return nil, nil // no port to pin against
	}
	return &urlPin{host: host, port: port, ip: ips[0].String()}, nil
}

func defaultPort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// matchHost reports whether host matches any allowlist pattern. Matching is
// literal — never glob — so dots are significant. A pattern is either an exact
// host ("api.github.com") or a leading-dot suffix (".github.com") that matches
// that domain and any subdomain. Wildcards are rejected at config-load time
// (ValidateHostPattern) because filepath-style "*" also crosses dots, silently
// widening the allowlist.
func matchHost(patterns []string, host string) bool {
	host = strings.ToLower(host)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if strings.HasPrefix(p, ".") {
			if host == p[1:] || strings.HasSuffix(host, p) {
				return true
			}
			continue
		}
		if host == p {
			return true
		}
	}
	return false
}

// ValidateHostPattern rejects URL host-allowlist patterns containing glob
// metacharacters, which are a footgun: "*.github.io" also matches
// "a.b.evil.github.io". Use an exact host or a leading-dot suffix (".github.io").
func ValidateHostPattern(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("empty host pattern")
	}
	if strings.ContainsAny(p, "*?[]") {
		return fmt.Errorf("host pattern %q must not contain wildcards; use an exact host or a leading-dot suffix like %q", p, ".github.io")
	}
	return nil
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
