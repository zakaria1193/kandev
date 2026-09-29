// fork(slack-clarify): see FORK.md. Lets an allow-listed plugin answer
// clarification bundles through the Host API instead of the exact-receipt
// path (ADR 0052).
//
// Upstream refuses AnswerClarification unconditionally: only the native UI
// can prove it observed the exact revision that a human response receipt
// requires (host_interactions.go's exactHumanResponseRequired). The Slack
// plugin has no such receipt — it relays a human's reply from a thread — so
// with this seam off it must fall back to a REST call carrying a personal
// access token. That is worse for the operator (another credential to
// manage) and no safer: the plugin's own Slack-user allow-list is already the
// human gate, on either path.
//
// Off by default. Turn it on per plugin id with
// KANDEV_FEATURES_PLUGIN_DELEGATED_CLARIFICATION=<comma-separated plugin ids>,
// e.g. KANDEV_FEATURES_PLUGIN_DELEGATED_CLARIFICATION=kandev-plugin-slack.
// There is no wildcard: an id not in the list gets upstream's behaviour.
//
// Not delegated: RespondToPermission and CancelClarification stay exact-only,
// and this path still runs through interactionWriteTarget, so a plugin
// missing api_write:interactions is denied before it ever reaches here.
package plugins

import (
	"context"
	"fmt"
	"os"
	"strings"

	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/pkg/pluginsdk"
)

// DelegatedClarificationEnv lists the plugin ids allowed to answer
// clarification bundles through Interactions().AnswerClarification.
const DelegatedClarificationEnv = "KANDEV_FEATURES_PLUGIN_DELEGATED_CLARIFICATION"

// parseDelegatedClarificationPlugins splits the env var's comma-separated
// plugin ids, trimming whitespace and dropping empty entries.
func parseDelegatedClarificationPlugins(raw string) []string {
	var ids []string
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// forkDelegatedClarificationAllowed reports whether pluginID is on the
// allow-list. It re-reads the env var on every call, like the other fork
// flags, so tests can use t.Setenv without a process restart. There is no
// wildcard: matching is exact and case-sensitive.
func forkDelegatedClarificationAllowed(pluginID string) bool {
	for _, id := range parseDelegatedClarificationPlugins(os.Getenv(DelegatedClarificationEnv)) {
		if id == pluginID {
			return true
		}
	}
	return false
}

// forkAnswerClarification is the delegated path: it mirrors the pre-exact
// implementation of AnswerClarification, minus the human response receipt
// that only the native UI can supply.
func (r interactionReader) forkAnswerClarification(
	ctx context.Context, in pluginsdk.ClarificationResponse,
) (*pluginsdk.Interaction, error) {
	responder, err := r.host.interactionWriteTarget()
	if err != nil {
		return nil, err
	}
	if responder == nil {
		return r.host.UnimplementedHostData.Interactions().AnswerClarification(ctx, in)
	}
	interaction, err := r.host.answerableInteraction(ctx, in.InteractionID, taskmodels.InteractionKindClarification)
	if err != nil {
		return nil, err
	}
	if len(in.Answers) == 0 {
		return nil, invalidArgument("answers is required")
	}
	answers := make([]PluginClarificationAnswer, len(in.Answers))
	for i, answer := range in.Answers {
		if answer.QuestionID == "" {
			return nil, invalidArgument(fmt.Sprintf("answer %d is missing question_id", i+1))
		}
		answers[i] = PluginClarificationAnswer{
			QuestionID:      answer.QuestionID,
			SelectedOptions: answer.SelectedOptions,
			CustomText:      answer.CustomText,
		}
	}
	if err := responder.AnswerClarification(ctx, interaction.ID, answers); err != nil {
		return nil, err
	}
	return r.host.reloadInteraction(ctx, interaction, taskmodels.InteractionStatusAnswered)
}
