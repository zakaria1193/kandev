package lifecycle

// fork(unlisted-model) tests. See FORK.md.

import (
	"context"
	"errors"
	"testing"
)

func TestForkUnlistedModelOffKeepsUpstreamBehaviour(t *testing.T) {
	t.Setenv(ForkUnlistedModelsEnv, "")
	applier := &fakeModelApplier{}
	decision, err := applyStartModelPolicy(context.Background(), newPolicyTestLogger(), applier,
		modelState("opus", "sonnet"), StartModelPolicy{Model: "claude-opus-5-5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applier.calls) != 0 {
		t.Fatalf("SetModel must not be called for an unlisted model when the flag is off, got %v", applier.calls)
	}
	if decision.Outcome != ModelSelectionOutcomeProviderDefault {
		t.Fatalf("outcome = %q, want provider default", decision.Outcome)
	}
}

func TestForkUnlistedModelAppliedWhenExecutorAccepts(t *testing.T) {
	t.Setenv(ForkUnlistedModelsEnv, "true")
	applier := &fakeModelApplier{}
	decision, err := applyStartModelPolicy(context.Background(), newPolicyTestLogger(), applier,
		modelState("opus", "sonnet"), StartModelPolicy{Model: "claude-opus-5-5", RequireExactModel: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applier.calls) != 1 || applier.calls[0] != "claude-opus-5-5" {
		t.Fatalf("SetModel calls = %v", applier.calls)
	}
	if decision.Outcome != ModelSelectionOutcomeApplied || decision.EffectiveModel != "claude-opus-5-5" ||
		decision.Reason != ModelSelectionReasonUnlistedModelApplied || decision.Warning {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestForkUnlistedModelRefusedFallsBackToUpstreamRules(t *testing.T) {
	t.Setenv(ForkUnlistedModelsEnv, "1")
	applier := &fakeModelApplier{errs: []error{errors.New("unknown model"), nil}}
	decision, err := applyStartModelPolicy(context.Background(), newPolicyTestLogger(), applier,
		modelState("opus", "sonnet"), StartModelPolicy{Model: "claude-opus-5-5", FallbackModel: "opus"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(applier.calls) != 2 || applier.calls[1] != "opus" {
		t.Fatalf("expected the unlisted try then the advertised fallback, got %v", applier.calls)
	}
	if decision.Outcome != ModelSelectionOutcomeExplicitFallback {
		t.Fatalf("outcome = %q, want explicit fallback", decision.Outcome)
	}
}
