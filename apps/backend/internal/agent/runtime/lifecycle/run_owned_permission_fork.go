package lifecycle

// fork(office-schedule): see FORK.md. Answers permission requests raised by
// run-owned (taskless) Office executions, such as a CEO routine wake.
//
// Task sessions surface a permission request to a person through the task
// chat, and auto-approved requests are resolved only after the orchestrator
// persisted the decision on that task session. A run-owned execution has no
// task and no task session, so the orchestrator drops the event ("missing
// task_id") and nothing ever answers it: the agent stops at its first native
// tool call that needs permission and the run stays claimed until a stall
// sweep or a workspace pause. With the flag on:
//
//   - an auto-approve candidate (profile auto_approve=true) is resolved with
//     the option agentctl already selected;
//   - any other request is cancelled, so the agent sees a refusal and can
//     carry on instead of waiting for an answer nobody can give.
//
// Off by default; turn it on with KANDEV_FORK_OFFICE_TASKLESS_PERMISSIONS=true.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"go.uber.org/zap"
)

// ForkOfficeTasklessPermissionsEnv enables answering run-owned permission requests.
const ForkOfficeTasklessPermissionsEnv = "KANDEV_FORK_OFFICE_TASKLESS_PERMISSIONS"

func forkOfficeTasklessPermissionsEnabled() bool {
	on, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(ForkOfficeTasklessPermissionsEnv)))
	return err == nil && on
}

type runOwnedPermissionAction int

const (
	runOwnedPermissionIgnore runOwnedPermissionAction = iota
	runOwnedPermissionResolve
	runOwnedPermissionCancel
)

// decideRunOwnedPermission is the pure decision: which answer, if any, the
// backend gives a permission request from this owner.
func decideRunOwnedPermission(enabled bool, owner ExecutionOwner, event agentctl.AgentEvent) runOwnedPermissionAction {
	if !enabled || owner.Kind != ExecutionOwnerRun || owner.TaskID != "" {
		return runOwnedPermissionIgnore
	}
	if event.RequestID == "" || event.PendingID == "" {
		return runOwnedPermissionIgnore
	}
	if event.AutoApprovedOptionID != "" && event.AutoApprovalPending {
		return runOwnedPermissionResolve
	}
	if event.AutoApprovedOptionID != "" {
		return runOwnedPermissionIgnore // agentctl already answered it (older agentctl)
	}
	return runOwnedPermissionCancel
}

// runOwnedPermissionClient is the part of *agentctl.Client this seam uses.
type runOwnedPermissionClient interface {
	ResolvePermission(ctx context.Context, requestID, pendingID, optionID string) (*agentctl.PermissionResolveResponse, error)
	CancelPermission(ctx context.Context, requestID, pendingID string) (*agentctl.PermissionCancelResponse, error)
}

func answerRunOwnedPermission(
	ctx context.Context, client runOwnedPermissionClient,
	action runOwnedPermissionAction, event agentctl.AgentEvent,
) error {
	switch action {
	case runOwnedPermissionResolve:
		_, err := client.ResolvePermission(ctx, event.RequestID, event.PendingID, event.AutoApprovedOptionID)
		return err
	case runOwnedPermissionCancel:
		_, err := client.CancelPermission(ctx, event.RequestID, event.PendingID)
		return err
	}
	return nil
}

// forkAnswerRunOwnedPermission answers the request asynchronously so the
// agent event loop is never blocked on the agentctl round trip.
func (m *Manager) forkAnswerRunOwnedPermission(execution *AgentExecution, event agentctl.AgentEvent) {
	owner := execution.OwnerSnapshot()
	action := decideRunOwnedPermission(forkOfficeTasklessPermissionsEnabled(), owner, event)
	if action == runOwnedPermissionIgnore {
		return
	}
	fields := []zap.Field{
		zap.String("execution_id", execution.ID),
		zap.String("run_id", owner.RunID),
		zap.String("run_session_id", owner.RunSessionID),
		zap.String("pending_id", event.PendingID),
		zap.String("title", event.PermissionTitle),
		zap.Bool("approve", action == runOwnedPermissionResolve),
	}
	go func() {
		client, release := execution.AcquireAgentCtlClient()
		defer release()
		if client == nil {
			m.logger.Warn("run-owned permission left unanswered: no agentctl client", fields...)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := answerRunOwnedPermission(ctx, client, action, event); err != nil {
			m.logger.Warn("run-owned permission answer failed", append(fields, zap.Error(err))...)
			return
		}
		m.logger.Info("answered run-owned permission request", fields...)
	}()
}
