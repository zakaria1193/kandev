package lifecycle

// fork(unlisted-model): see FORK.md. Lets a profile run a model id that the
// agent CLI accepts but its ACP catalog does not advertise yet (for example a
// model released after the adapter's catalog was built). Off by default; turn
// it on with KANDEV_FORK_UNLISTED_MODELS=true.

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/kandev/kandev/internal/common/logger"
	"go.uber.org/zap"
)

// ForkUnlistedModelsEnv enables trying non-advertised profile models.
const ForkUnlistedModelsEnv = "KANDEV_FORK_UNLISTED_MODELS"

// ModelSelectionReasonUnlistedModelApplied marks a model applied although
// the executor catalog did not list it.
const ModelSelectionReasonUnlistedModelApplied = "unlisted_model_applied"

func forkUnlistedModelsEnabled() bool {
	on, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(ForkUnlistedModelsEnv)))
	return err == nil && on
}

// tryUnlistedModel asks the executor to switch to a model its catalog does
// not advertise. ok is false when the fork flag is off or the executor
// refuses, and the caller then continues with upstream's fallback rules.
func tryUnlistedModel(
	ctx context.Context,
	log *logger.Logger,
	applier modelApplier,
	policy StartModelPolicy,
	decision ModelSelectionDecision,
) (ModelSelectionDecision, bool) {
	if !forkUnlistedModelsEnabled() || strings.TrimSpace(policy.Model) == "" {
		return decision, false
	}
	if err := applier.SetModel(ctx, policy.Model); err != nil {
		log.Info("unlisted profile model refused by executor, using catalog rules",
			zap.String("model", policy.Model), zap.Error(err))
		return decision, false
	}
	decision.SetModelCalled = true
	decision.EffectiveModel = policy.Model
	decision.Outcome = ModelSelectionOutcomeApplied
	decision.Reason = ModelSelectionReasonUnlistedModelApplied
	return decision, true
}
