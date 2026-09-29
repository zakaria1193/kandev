package routingerr

import "github.com/kandev/kandev/internal/agent/managedruntime"

// fork(office-schedule): resolve the managed npm project marker before the
// routing recovery probe spawns the provider CLI.
//
// Real launches resolve the "~/.kandev/managed-npm-runtime" marker inside
// agentctl, but the /routing/retry probe spawns the resolver's command straight from the
// backend. Without this step npm receives the literal marker, fails with
// ENOENT, and the provider is classified provider_not_configured
// (user_action_required). Every later retry fails the same way, so routed
// Office runs stay parked forever after one transient launch failure.
//
// A prefix that cannot be prepared returns ok=false and the probe fails
// with an explicit message instead of running npm against an
// unresolved marker.
func prepareProbeArgs(args []string) ([]string, bool) {
	prepared := append([]string(nil), args...)
	if err := managedruntime.PrepareNPMProjectPrefix(prepared); err != nil {
		return nil, false
	}
	return prepared, true
}
