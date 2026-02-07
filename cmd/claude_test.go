package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseClaudeArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantPath   string // just the basename, since resolvePath makes it absolute
		wantClaude []string
	}{
		{
			name:       "no args",
			args:       nil,
			wantPath:   ".",
			wantClaude: nil,
		},
		{
			name:       "path only",
			args:       []string{"/tmp/proj"},
			wantPath:   "/tmp/proj",
			wantClaude: nil,
		},
		{
			name:       "path with separator and claude args",
			args:       []string{"/tmp/proj", "--", "-p", "fix tests"},
			wantPath:   "/tmp/proj",
			wantClaude: []string{"-p", "fix tests"},
		},
		{
			name:       "flags without separator are positional and ignored",
			args:       []string{"-p", "do stuff"},
			wantPath:   ".",
			wantClaude: nil, // no "--" so nothing goes to claudeArgs; "-p" starts with "-" so not used as path
		},
		{
			name:       "separator with flags and default path",
			args:       []string{"--", "-p", "do stuff"},
			wantPath:   ".",
			wantClaude: []string{"-p", "do stuff"},
		},
		{
			name:       "dot path with claude args",
			args:       []string{".", "--", "--verbose", "-p", "hello"},
			wantPath:   ".",
			wantClaude: []string{"--verbose", "-p", "hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotClaude := parseClaudeArgs(tt.args)

			// Check path (resolvePath makes things absolute, so compare appropriately)
			if tt.wantPath == "." {
				// Should resolve to cwd
				if !filepath.IsAbs(gotPath) {
					t.Errorf("path = %q, want absolute", gotPath)
				}
			} else if tt.wantPath != gotPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}

			// Check claude args
			if !stringSliceEqual(gotClaude, tt.wantClaude) {
				t.Errorf("claudeArgs = %v, want %v", gotClaude, tt.wantClaude)
			}
		})
	}
}

func TestParseClaudeArgsMultipleSeparators(t *testing.T) {
	// Both "--" are consumed as separators; everything after the first
	// separator goes to claude args (subsequent "--" are also swallowed)
	args := []string{"/tmp/proj", "--", "--", "-p", "hello"}
	path, claudeArgs := parseClaudeArgs(args)

	if path != "/tmp/proj" {
		t.Errorf("path = %q, want /tmp/proj", path)
	}

	want := []string{"-p", "hello"}
	if !stringSliceEqual(claudeArgs, want) {
		t.Errorf("claudeArgs = %v, want %v", claudeArgs, want)
	}
}

func TestParseClaudeArgsOnlySeparator(t *testing.T) {
	// Just "--" with nothing else: default path, empty claude args
	args := []string{"--"}
	path, claudeArgs := parseClaudeArgs(args)

	if !filepath.IsAbs(path) {
		t.Errorf("path = %q, want absolute", path)
	}
	if len(claudeArgs) != 0 {
		t.Errorf("claudeArgs = %v, want empty", claudeArgs)
	}
}

func TestParseClaudeArgsEmptyString(t *testing.T) {
	// An empty string arg doesn't start with "-", so it's treated as the path
	args := []string{""}
	path, claudeArgs := parseClaudeArgs(args)

	// resolvePath("") resolves to cwd
	if !filepath.IsAbs(path) {
		t.Errorf("path = %q, want absolute", path)
	}
	if len(claudeArgs) != 0 {
		t.Errorf("claudeArgs = %v, want empty", claudeArgs)
	}
}

func TestParseClaudeArgsExtraPositionalIgnored(t *testing.T) {
	// Only the first non-flag positional arg is used as the path;
	// additional positional args before "--" are silently dropped.
	args := []string{"/tmp/proj", "extra-arg", "--", "-p", "hello"}
	path, claudeArgs := parseClaudeArgs(args)

	if path != "/tmp/proj" {
		t.Errorf("path = %q, want /tmp/proj", path)
	}
	want := []string{"-p", "hello"}
	if !stringSliceEqual(claudeArgs, want) {
		t.Errorf("claudeArgs = %v, want %v", claudeArgs, want)
	}
}

func TestParseClaudeArgsDoesNotTreatFlagAsPath(t *testing.T) {
	args := []string{"-p", "hello"}
	path, _ := parseClaudeArgs(args)

	// -p starts with "-", so it should not be treated as a path
	if strings.HasSuffix(path, "-p") {
		t.Errorf("flag -p was incorrectly treated as a path: %q", path)
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
