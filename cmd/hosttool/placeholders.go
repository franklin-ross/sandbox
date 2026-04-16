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
	for _, a := range ht.Args {
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("arg with empty name")
		}
		if declared[a.Name] {
			return fmt.Errorf("duplicate arg %q", a.Name)
		}
		declared[a.Name] = true
	}
	used := make(map[string]bool)
	for _, m := range placeholderRE.FindAllStringSubmatch(ht.Cmd, -1) {
		name := m[1]
		if !declared[name] {
			return fmt.Errorf("cmd references undeclared arg ${%s}", name)
		}
		used[name] = true
	}
	for name := range declared {
		if !used[name] {
			fmt.Fprintf(os.Stderr, "warning: host_tool %q: arg %q declared but not used in cmd\n", ht.Name, name)
		}
	}
	return nil
}
