package extract

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
)

// FindTool returns the first existing path among configured and default
// candidates, or looks the name up on PATH. It returns "" if none exist.
func FindTool(configured string, name string, candidates ...string) string {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured
		}
		return ""
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// RunTool executes a command, returning stdout. On failure the error
// includes the first line of stderr. The tool must not be given any path
// under the scan root as an output location; callers pass a temp dir.
func RunTool(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if msg == "" {
			msg = err.Error()
		}
		return out, &ToolError{Bin: bin, Msg: msg}
	}
	return out, nil
}

// ToolError is a failed external command.
type ToolError struct {
	Bin string
	Msg string
}

func (e *ToolError) Error() string { return e.Bin + ": " + e.Msg }

// VersionLine runs bin with args and returns the first non-empty output line.
func VersionLine(ctx context.Context, bin string, args ...string) string {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			return l
		}
	}
	return ""
}
