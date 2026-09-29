package acp

// fork(unlisted-model) tests. See FORK.md.

import (
	"context"
	"errors"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
)

type forkModelConn struct {
	configReqs []acpsdk.SetSessionConfigOptionRequest
	modelReqs  []acpsdk.UnstableSetSessionModelRequest
	err        error
}

func (f *forkModelConn) SetSessionConfigOption(
	_ context.Context,
	req acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	f.configReqs = append(f.configReqs, req)
	return acpsdk.SetSessionConfigOptionResponse{}, f.err
}

func (f *forkModelConn) UnstableSetSessionModel(
	_ context.Context,
	req acpsdk.UnstableSetSessionModelRequest,
) (acpsdk.UnstableSetSessionModelResponse, error) {
	f.modelReqs = append(f.modelReqs, req)
	return acpsdk.UnstableSetSessionModelResponse{}, f.err
}

// claudeAdapterForFork mirrors a claude-agent-acp session: a catalog of
// aliases and a typed "model" select config option.
func claudeAdapterForFork() *Adapter {
	a := newTestAdapterForAgent(claudeAgentID)
	a.sessionID = "sess-claude"
	a.availableModels = []modelInfo{
		{ModelId: "default", Name: "Default"},
		{ModelId: "sonnet", Name: "Sonnet"},
	}
	a.availableConfigOptions = []streams.ConfigOption{{
		Type: "select", ID: "model", Category: "model", CurrentValue: "default",
		Options: []streams.ConfigOptionValue{
			{Value: "default", Name: "Default"},
			{Value: "sonnet", Name: "Sonnet"},
		},
	}}
	return a
}

func TestForkUnlistedModelOffRefusesLocally(t *testing.T) {
	t.Setenv(forkUnlistedModelsEnv, "")
	a := claudeAdapterForFork()
	conn := &forkModelConn{}

	err := a.setModelWithConn(context.Background(), conn, "sess-claude", "claude-opus-5-5")
	if err == nil || !strings.Contains(err.Error(), "not in the agent's") {
		t.Fatalf("error = %v, want the upstream catalog refusal", err)
	}
	if len(conn.configReqs)+len(conn.modelReqs) != 0 {
		t.Fatalf("no RPC may reach the agent when the flag is off, got %d config + %d model",
			len(conn.configReqs), len(conn.modelReqs))
	}
}

func TestForkUnlistedModelOnAsksTheAgent(t *testing.T) {
	t.Setenv(forkUnlistedModelsEnv, "true")
	a := claudeAdapterForFork()
	conn := &forkModelConn{}

	if err := a.setModelWithConn(context.Background(), conn, "sess-claude", "claude-opus-5-5"); err != nil {
		t.Fatalf("setModel: %v", err)
	}
	if len(conn.configReqs) != 1 {
		t.Fatalf("set_config_option calls = %d, want 1", len(conn.configReqs))
	}
	req := conn.configReqs[0]
	if req.ValueId == nil || string(req.ValueId.ConfigId) != "model" || string(req.ValueId.Value) != "claude-opus-5-5" {
		t.Fatalf("set_config_option request = %+v, want model=claude-opus-5-5", req)
	}
}

func TestForkUnlistedModelOnPropagatesAgentRefusal(t *testing.T) {
	t.Setenv(forkUnlistedModelsEnv, "1")
	a := claudeAdapterForFork()
	conn := &forkModelConn{err: errors.New("Invalid value for config option model: nope")}

	err := a.setModelWithConn(context.Background(), conn, "sess-claude", "nope")
	if err == nil || !strings.Contains(err.Error(), "Invalid value") {
		t.Fatalf("error = %v, want the agent's refusal", err)
	}
	if got := currentModelFromConfig(a.availableConfigOptions); got != "default" {
		t.Fatalf("current model changed after a refused switch: %q", got)
	}
}

func TestForkUnlistedModelOnKeepsListedModelPath(t *testing.T) {
	t.Setenv(forkUnlistedModelsEnv, "true")
	a := claudeAdapterForFork()
	conn := &forkModelConn{}

	if err := a.setModelWithConn(context.Background(), conn, "sess-claude", "sonnet"); err != nil {
		t.Fatalf("setModel: %v", err)
	}
	if len(conn.configReqs) != 1 || string(conn.configReqs[0].ValueId.Value) != "sonnet" {
		t.Fatalf("set_config_option requests = %+v, want one for sonnet", conn.configReqs)
	}
}
