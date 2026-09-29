package routingerr

import (
	"slices"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/agent/managedruntime"
)

func TestPrepareProbeArgsResolvesManagedNPMPrefix(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	in := append([]string{"npx", "--yes"}, managedruntime.NPMProjectPrefixArgs()...)
	in = append(in, "@agentclientprotocol/claude-agent-acp@1.0.0")
	orig := slices.Clone(in)

	out, ok := prepareProbeArgs(in)
	if !ok {
		t.Fatal("prepareProbeArgs returned ok=false")
	}
	if !slices.Equal(in, orig) {
		t.Fatalf("input args mutated: %v", in)
	}
	idx := slices.Index(out, "--prefix")
	if idx < 0 || idx+1 >= len(out) {
		t.Fatalf("--prefix missing from %v", out)
	}
	if got := out[idx+1]; got == managedruntime.NPMProjectPrefix || strings.HasPrefix(got, "~") {
		t.Fatalf("prefix marker not resolved: %q", got)
	}
}

func TestPrepareProbeArgsLeavesUnmanagedCommandUnchanged(t *testing.T) {
	in := []string{"gemini", "--experimental-acp"}
	out, ok := prepareProbeArgs(in)
	if !ok || !slices.Equal(out, in) {
		t.Fatalf("got %v ok=%v, want %v ok=true", out, ok, in)
	}
}
