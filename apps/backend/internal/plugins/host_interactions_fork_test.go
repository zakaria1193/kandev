// fork(slack-clarify) tests. See FORK.md.
package plugins

import (
	"context"
	"reflect"
	"testing"

	"github.com/kandev/kandev/internal/plugins/manifest"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
)

const forkTestPluginID = "kandev-plugin-slack"

func answerClarificationRequest() pluginsdk.ClarificationResponse {
	return pluginsdk.ClarificationResponse{
		InteractionID: "pending-2",
		Answers:       []pluginsdk.ClarificationAnswer{{QuestionID: "q1", CustomText: "yes"}},
	}
}

// ── allow-list parsing ──────────────────────────────────────────────────

func TestParseDelegatedClarificationPlugins(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"single", "kandev-plugin-slack", []string{"kandev-plugin-slack"}},
		{"whitespace and empty entries", " kandev-plugin-slack , ,x", []string{"kandev-plugin-slack", "x"}},
		{"wildcard enables nothing", "*", []string{"*"}},
		{"true enables nothing", "true", []string{"true"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseDelegatedClarificationPlugins(test.raw)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseDelegatedClarificationPlugins(%q) = %#v, want %#v", test.raw, got, test.want)
			}
		})
	}
}

func TestForkDelegatedClarificationAllowed(t *testing.T) {
	t.Setenv(DelegatedClarificationEnv, "kandev-plugin-slack, other-plugin")

	if !forkDelegatedClarificationAllowed("kandev-plugin-slack") {
		t.Fatal("listed plugin id should be allowed")
	}
	if forkDelegatedClarificationAllowed("Kandev-Plugin-Slack") {
		t.Fatal("match must be case-sensitive")
	}
	if forkDelegatedClarificationAllowed("unlisted-plugin") {
		t.Fatal("unlisted plugin id should not be allowed")
	}

	t.Setenv(DelegatedClarificationEnv, "*")
	if forkDelegatedClarificationAllowed("any-plugin") {
		t.Fatal("* must not act as a wildcard")
	}

	t.Setenv(DelegatedClarificationEnv, "true")
	if forkDelegatedClarificationAllowed("any-plugin") {
		t.Fatal("true must not act as a wildcard")
	}
}

// ── gating: flag unset or plugin unlisted ──────────────────────────────

func TestForkAnswerClarification_FlagUnsetDeniesLikeUpstream(t *testing.T) {
	d := newTestDataHost(readWriteCaps())
	d.host.pluginID = forkTestPluginID
	withInteraction(d, pendingClarificationInteraction())

	_, err := d.host.Interactions().AnswerClarification(context.Background(), answerClarificationRequest())
	assertCode(t, err, codes.PermissionDenied)
	if d.responder.writeCalled {
		t.Fatal("responder reached while the flag is unset")
	}
}

func TestForkAnswerClarification_PluginNotListedDeniesLikeUpstream(t *testing.T) {
	t.Setenv(DelegatedClarificationEnv, "other-plugin")

	d := newTestDataHost(readWriteCaps())
	d.host.pluginID = forkTestPluginID
	withInteraction(d, pendingClarificationInteraction())

	_, err := d.host.Interactions().AnswerClarification(context.Background(), answerClarificationRequest())
	assertCode(t, err, codes.PermissionDenied)
	if d.responder.writeCalled {
		t.Fatal("responder reached for an unlisted plugin id")
	}
}

// ── gating: listed but missing api_write:interactions ──────────────────

func TestForkAnswerClarification_ListedWithoutWriteCapabilityDenied(t *testing.T) {
	t.Setenv(DelegatedClarificationEnv, forkTestPluginID)

	tests := []struct {
		name string
		caps manifest.Capabilities
	}{
		{"no capabilities", manifest.Capabilities{}},
		{"read-only", manifest.Capabilities{APIRead: []string{"interactions"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := newTestDataHost(test.caps)
			d.host.pluginID = forkTestPluginID
			withInteraction(d, pendingClarificationInteraction())

			_, err := d.host.Interactions().AnswerClarification(context.Background(), answerClarificationRequest())
			assertPermissionDenied(t, err, "api_write:interactions")
			if d.responder.writeCalled {
				t.Fatal("responder reached despite missing api_write:interactions")
			}
		})
	}
}

// ── gating: listed but unwired ───────────────────────────────────────────

func TestForkAnswerClarification_ListedButUnwiredIsUnimplemented(t *testing.T) {
	t.Setenv(DelegatedClarificationEnv, forkTestPluginID)

	host := &pluginHost{pluginID: forkTestPluginID, capabilities: readWriteCaps()}
	_, err := host.Interactions().AnswerClarification(context.Background(), answerClarificationRequest())
	assertCode(t, err, codes.Unimplemented)
}

// ── gating: other interaction methods stay exact-only ───────────────────

func TestForkAnswerClarification_OtherMethodsStillDeniedForListedPlugin(t *testing.T) {
	t.Setenv(DelegatedClarificationEnv, forkTestPluginID)

	d := newTestDataHost(readWriteCaps())
	d.host.pluginID = forkTestPluginID
	withInteraction(d, pendingPermissionInteraction())
	ctx := context.Background()

	_, err := d.host.Interactions().RespondToPermission(ctx, pluginsdk.PermissionResponse{
		InteractionID: "pending-1", OptionID: "allow",
	})
	assertCode(t, err, codes.PermissionDenied)

	_, err = d.host.Interactions().CancelClarification(ctx, "pending-2", "nope")
	assertCode(t, err, codes.PermissionDenied)

	if d.responder.writeCalled {
		t.Fatal("responder reached through a still-exact-only method")
	}
}
