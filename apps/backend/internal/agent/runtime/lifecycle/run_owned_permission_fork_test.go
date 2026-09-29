package lifecycle

import (
	"context"
	"testing"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
)

func runOwner() ExecutionOwner {
	return ExecutionOwner{Kind: ExecutionOwnerRun, WorkspaceID: "ws", RunID: "run-1", RunSessionID: "rs-1", Attempt: 1}
}

func permEvent(autoOption string, pending bool) agentctl.AgentEvent {
	return agentctl.AgentEvent{
		Type: "permission_request", RequestID: "req-1", PendingID: "pend-1",
		AutoApprovedOptionID: autoOption, AutoApprovalPending: pending,
	}
}

func TestDecideRunOwnedPermission(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		owner   ExecutionOwner
		event   agentctl.AgentEvent
		want    runOwnedPermissionAction
	}{
		{"flag off", false, runOwner(), permEvent("allow", true), runOwnedPermissionIgnore},
		{"task owner untouched", true, ExecutionOwner{Kind: ExecutionOwnerTask, TaskID: "t", SessionID: "s"}, permEvent("allow", true), runOwnedPermissionIgnore},
		{"run owner with task id untouched", true, ExecutionOwner{Kind: ExecutionOwnerRun, TaskID: "t"}, permEvent("allow", true), runOwnedPermissionIgnore},
		{"auto-approve candidate resolved", true, runOwner(), permEvent("allow", true), runOwnedPermissionResolve},
		{"already approved by old agentctl", true, runOwner(), permEvent("allow", false), runOwnedPermissionIgnore},
		{"no auto approve is cancelled", true, runOwner(), permEvent("", false), runOwnedPermissionCancel},
		{"missing ids ignored", true, runOwner(), agentctl.AgentEvent{Type: "permission_request"}, runOwnedPermissionIgnore},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideRunOwnedPermission(tc.enabled, tc.owner, tc.event); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

type fakePermissionClient struct {
	resolved, cancelled []string
}

func (f *fakePermissionClient) ResolvePermission(_ context.Context, requestID, pendingID, optionID string) (*agentctl.PermissionResolveResponse, error) {
	f.resolved = append(f.resolved, requestID+"/"+pendingID+"/"+optionID)
	return &agentctl.PermissionResolveResponse{}, nil
}

func (f *fakePermissionClient) CancelPermission(_ context.Context, requestID, pendingID string) (*agentctl.PermissionCancelResponse, error) {
	f.cancelled = append(f.cancelled, requestID+"/"+pendingID)
	return &agentctl.PermissionCancelResponse{}, nil
}

func TestAnswerRunOwnedPermission(t *testing.T) {
	f := &fakePermissionClient{}
	ctx := context.Background()
	if err := answerRunOwnedPermission(ctx, f, runOwnedPermissionResolve, permEvent("allow_once", true)); err != nil {
		t.Fatal(err)
	}
	if err := answerRunOwnedPermission(ctx, f, runOwnedPermissionCancel, permEvent("", false)); err != nil {
		t.Fatal(err)
	}
	if err := answerRunOwnedPermission(ctx, f, runOwnedPermissionIgnore, permEvent("", false)); err != nil {
		t.Fatal(err)
	}
	if len(f.resolved) != 1 || f.resolved[0] != "req-1/pend-1/allow_once" {
		t.Fatalf("resolved = %v", f.resolved)
	}
	if len(f.cancelled) != 1 || f.cancelled[0] != "req-1/pend-1" {
		t.Fatalf("cancelled = %v", f.cancelled)
	}
}

func TestForkOfficeTasklessPermissionsEnabled(t *testing.T) {
	t.Setenv(ForkOfficeTasklessPermissionsEnv, "")
	if forkOfficeTasklessPermissionsEnabled() {
		t.Fatal("default must be off")
	}
	t.Setenv(ForkOfficeTasklessPermissionsEnv, "true")
	if !forkOfficeTasklessPermissionsEnabled() {
		t.Fatal("true must enable")
	}
}
