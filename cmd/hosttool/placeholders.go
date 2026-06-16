package hosttool

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// placeholderRE matches ${name} where name is [A-Za-z_][A-Za-z0-9_]*.
var placeholderRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ValidatePlaceholders checks that every ${name} reference in ht.Cmd refers to
// a declared arg, that no arg names are empty or duplicated, and warns about
// declared args not used in the cmd.
func ValidatePlaceholders(ht Tool) error {
	declared := make(map[string]bool, len(ht.Args))
	var urlArgs []string
	for _, a := range ht.Args {
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("arg with empty name")
		}
		if declared[a.Name] {
			return fmt.Errorf("duplicate arg %q", a.Name)
		}
		declared[a.Name] = true
		if a.URL != nil {
			urlArgs = append(urlArgs, a.Name)
			for _, p := range a.URL.Hosts {
				if err := ValidateHostPattern(p); err != nil {
					return fmt.Errorf("arg %q: %w", a.Name, err)
				}
			}
			// An allowlist is the real boundary. Without one the agent can pass
			// any hostname, so blocking private IPs alone is bypassable via DNS
			// rebinding (a domain whose DNS flips to a private IP after the
			// check). Nudge authors to constrain destinations.
			blockOn := a.URL.BlockPrivateIPs == nil || *a.URL.BlockPrivateIPs
			if blockOn && len(a.URL.Hosts) == 0 {
				fmt.Fprintf(os.Stderr, "warning: host_tool %q: url arg %q has no host allowlist; the agent can pass any URL, leaving the host open to SSRF/DNS-rebinding. Add url.hosts (e.g. [\".github.io\"]) to restrict destinations.\n", ht.Name, a.Name)
			}
		}
	}

	// Each URL arg exposes derived placeholders that pin the validated address
	// (${<name>_resolve}, ${<name>_ip}, ${<name>_host}, ${<name>_port}). Allow
	// them in the cmd, and make sure none collides with a declared arg.
	allowedDerived := make(map[string]bool, len(urlArgs)*len(urlDerivedSuffixes))
	for _, name := range urlArgs {
		for _, suffix := range urlDerivedSuffixes {
			d := name + suffix
			if declared[d] {
				return fmt.Errorf("arg %q collides with the derived placeholder ${%s} of URL arg %q", d, d, name)
			}
			allowedDerived[d] = true
		}
	}

	used := make(map[string]bool)
	for _, m := range placeholderRE.FindAllStringSubmatch(ht.Cmd, -1) {
		name := m[1]
		if declared[name] {
			used[name] = true
			continue
		}
		if allowedDerived[name] {
			continue
		}
		return fmt.Errorf("cmd references undeclared arg ${%s}", name)
	}
	for name := range declared {
		if !used[name] {
			fmt.Fprintf(os.Stderr, "warning: host_tool %q: arg %q declared but not used in cmd\n", ht.Name, name)
		}
	}
	return nil
}
