package backendapp

// fork(slack-notify): wires step-change notifications and the Slack provider.
//
// No upstream event is added: task.step_entered is derived from the existing
// task.updated event, which carries the step-transition ledger id whenever a
// write moved the task to another workflow step, and task.completed from
// task.state_changed. Both only reach providers subscribed to them.

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	notificationservice "github.com/kandev/kandev/internal/notifications/service"
	"github.com/kandev/kandev/internal/secrets"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	workflowmodels "github.com/kandev/kandev/internal/workflow/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"go.uber.org/zap"
)

type forkStepGetter interface {
	GetStep(ctx context.Context, id string) (*workflowmodels.WorkflowStep, error)
}

type forkWorkflowGetter interface {
	GetWorkflow(ctx context.Context, id string) (*taskmodels.Workflow, error)
}

// wireForkSlackNotify subscribes the step-change notifications and gives the
// Slack provider its task resolver and secret store. Safe with nil inputs.
func wireForkSlackNotify(
	log *logger.Logger,
	eventBus bus.EventBus,
	notificationSvc *notificationservice.Service,
	steps forkStepGetter,
	workflows forkWorkflowGetter,
	secretStore secrets.SecretStore,
) {
	if notificationSvc == nil {
		return
	}
	notificationSvc.ConfigureSlack(slackSecretRevealer(secretStore))
	if eventBus == nil {
		return
	}
	lookups := forkSlackLookups(steps, workflows)
	if _, err := eventBus.Subscribe(events.TaskUpdated, func(ctx context.Context, event *bus.Event) error {
		if stepEvent, ok := taskStepEnteredFromEvent(event); ok {
			notificationSvc.HandleTaskStepEntered(ctx, stepEvent, lookups)
		}
		return nil
	}); err != nil {
		log.Error("Failed to subscribe to task step notifications", zap.Error(err))
	}
	if _, err := eventBus.Subscribe(events.TaskStateChanged, func(ctx context.Context, event *bus.Event) error {
		if completed, ok := taskCompletedFromEvent(event); ok {
			notificationSvc.HandleTaskCompleted(ctx, completed, lookups)
		}
		return nil
	}); err != nil {
		log.Error("Failed to subscribe to task completion notifications", zap.Error(err))
	}
}

func forkSlackLookups(steps forkStepGetter, workflows forkWorkflowGetter) notificationservice.SlackLookups {
	var lookups notificationservice.SlackLookups
	if steps != nil {
		lookups.StepName = func(ctx context.Context, id string) string {
			step, err := steps.GetStep(ctx, id)
			if err != nil || step == nil {
				return ""
			}
			return step.Name
		}
	}
	if workflows != nil {
		lookups.WorkflowName = func(ctx context.Context, id string) string {
			workflow, err := workflows.GetWorkflow(ctx, id)
			if err != nil || workflow == nil {
				return ""
			}
			return workflow.Name
		}
	}
	return lookups
}

// slackSecretRevealer reveals a user-visible (global, non-internal) secret as
// the provider's owner, so a provider can only use its owner's secrets.
func slackSecretRevealer(store secrets.SecretStore) func(ctx context.Context, userID, secretID string) (string, error) {
	if store == nil {
		return nil
	}
	visible := secrets.NewUserVisibleStore(store)
	return func(ctx context.Context, userID, secretID string) (string, error) {
		ownerCtx := authn.WithIdentity(ctx, authn.Identity{UserID: userID})
		return visible.Reveal(ownerCtx, secretID)
	}
}

func taskStepEnteredFromEvent(event *bus.Event) (notificationservice.TaskStepEntered, bool) {
	data := forkEventData(event)
	if data == nil {
		return notificationservice.TaskStepEntered{}, false
	}
	transitionID := forkInt64(data["step_transition_id"])
	stepEvent := notificationservice.TaskStepEntered{
		TaskID:       forkString(data, "task_id"),
		TaskTitle:    forkString(data, "title"),
		WorkspaceID:  forkString(data, "workspace_id"),
		WorkflowID:   forkString(data, "workflow_id"),
		StepID:       forkString(data, "workflow_step_id"),
		TransitionID: transitionID,
	}
	return stepEvent, transitionID > 0 && stepEvent.TaskID != "" && stepEvent.StepID != ""
}

func taskCompletedFromEvent(event *bus.Event) (notificationservice.TaskCompleted, bool) {
	data := forkEventData(event)
	if data == nil {
		return notificationservice.TaskCompleted{}, false
	}
	completed := string(v1.TaskStateCompleted)
	if forkString(data, "new_state") != completed || forkString(data, "old_state") == completed {
		return notificationservice.TaskCompleted{}, false
	}
	result := notificationservice.TaskCompleted{
		TaskID:      forkString(data, "task_id"),
		TaskTitle:   forkString(data, "title"),
		WorkspaceID: forkString(data, "workspace_id"),
		StepID:      forkString(data, "workflow_step_id"),
		CompletedAt: forkString(data, "updated_at"),
	}
	return result, result.TaskID != ""
}

// clarificationQuestionText returns the prompt of the clarification message
// that asked for input. A multi-question bundle notifies on its last message,
// so the count of the other questions is appended.
func clarificationQuestionText(data map[string]interface{}) string {
	question, _ := data["content"].(string)
	metadata, _ := data["metadata"].(map[string]interface{})
	total := forkInt64(metadata["question_total"])
	if question != "" && total > 1 {
		question += "\n(" + strconv.FormatInt(total-1, 10) + " more question(s) in Kandev)"
	}
	return question
}

func forkEventData(event *bus.Event) map[string]interface{} {
	if event == nil {
		return nil
	}
	data, _ := event.Data.(map[string]interface{})
	return data
}

func forkString(data map[string]interface{}, key string) string {
	value, _ := data[key].(string)
	return value
}

// forkInt64 accepts the numeric shapes an event field takes on the in-memory
// bus (int64/int) and after a JSON round trip over NATS (float64/json.Number).
func forkInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		if v > 0 && v < math.MaxInt64 {
			return int64(v)
		}
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}
