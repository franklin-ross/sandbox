package commands

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseClaudeArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantPath   string
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
			wantClaude: nil,
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

			if tt.wantPath == "." {
				if !filepath.IsAbs(gotPath) {
					t.Errorf("path = %q, want absolute", gotPath)
				}
			} else if tt.wantPath != gotPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}

			if !stringSliceEqual(gotClaude, tt.wantClaude) {
				t.Errorf("claudeArgs = %v, want %v", gotClaude, tt.wantClaude)
			}
		})
	}
}

func TestParseClaudeArgsMultipleSeparators(t *testing.T) {
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
	args := []string{""}
	path, claudeArgs := parseClaudeArgs(args)

	if !filepath.IsAbs(path) {
		t.Errorf("path = %q, want absolute", path)
	}
	if len(claudeArgs) != 0 {
		t.Errorf("claudeArgs = %v, want empty", claudeArgs)
	}
}

func TestParseClaudeArgsExtraPositionalIgnored(t *testing.T) {
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
