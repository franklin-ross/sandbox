package hosttool

import (
	"fmt"
	"strings"
)

// SubstituteCmd replaces ${name} placeholders in a command template with
// shell-quoted values. Unknown placeholders yield an error.
func SubstituteCmd(cmdTpl string, args map[string]string) (string, error) {
	var missingErr error
	out := placeholderRE.ReplaceAllStringFunc(cmdTpl, func(match string) string {
		name := match[2 : len(match)-1]
		v, ok := args[name]
		if !ok {
			if missingErr == nil {
				missingErr = fmt.Errorf("missing arg %q for placeholder", name)
			}
			return match
		}
		return shellQuote(v)
	})
	if missingErr != nil {
		return "", missingErr
	}
	return out, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
