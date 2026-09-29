package service

// fork(slack-notify): task step-change events and Slack provider wiring.
//
// Everything here is inert until a provider subscribes to the new events or a
// Slack provider is created: the handlers only fan out to providers that are
// enabled and subscribed, exactly like the upstream events.

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kandev/kandev/internal/notifications/models"
	"github.com/kandev/kandev/internal/notifications/providers"
	"go.uber.org/zap"
)

const (
	// EventTaskStepEntered fires once per workflow-step transition of a task.
	EventTaskStepEntered = providers.SlackEventStepEntered
	// EventTaskCompleted fires when a task's state becomes COMPLETED.
	EventTaskCompleted = providers.SlackEventTaskCompleted
)

// forkNotificationEvents are appended to AvailableEvents.
var forkNotificationEvents = []string{EventTaskStepEntered, EventTaskCompleted}

// TaskStepEntered describes one committed workflow-step transition.
// TransitionID is the step-transition ledger id; it makes the notification
// idempotent across duplicate task.updated publications.
type TaskStepEntered struct {
	TaskID       string
	TaskTitle    string
	WorkspaceID  string
	WorkflowID   string
	StepID       string
	TransitionID int64
}

// TaskCompleted describes a task whose state became COMPLETED. CompletedAt
// (the task's updated_at) keeps a re-opened and re-completed task notifying
// again while deduplicating repeated publications of the same completion.
type TaskCompleted struct {
	TaskID      string
	TaskTitle   string
	WorkspaceID string
	StepID      string
	CompletedAt string
}

// SlackLookups resolves names for step events. Every field is optional.
type SlackLookups struct {
	StepName     func(ctx context.Context, stepID string) string
	WorkflowName func(ctx context.Context, workflowID string) string
}

// HandleTaskStepEntered notifies subscribers of task.step_entered.
func (s *Service) HandleTaskStepEntered(ctx context.Context, event TaskStepEntered, lookups SlackLookups) {
	if event.TaskID == "" || event.TransitionID <= 0 {
		return
	}
	stepName := lookupName(ctx, lookups.StepName, event.StepID)
	workspaceName := s.workspaceName(ctx, event.WorkspaceID)
	payload := map[string]string{
		providers.SlackPayloadWorkspaceID:   event.WorkspaceID,
		providers.SlackPayloadWorkspaceName: workspaceName,
		providers.SlackPayloadWorkflowName:  lookupName(ctx, lookups.WorkflowName, event.WorkflowID),
		providers.SlackPayloadStepName:      stepName,
		providers.SlackPayloadTaskTitle:     event.TaskTitle,
		"workflow_id":                       event.WorkflowID,
		"step_id":                           event.StepID,
	}
	s.deliverTaskEvent(ctx, event.WorkspaceID, notificationPayload{
		TaskID:       event.TaskID,
		OccurrenceID: "step:" + strconv.FormatInt(event.TransitionID, 10),
		EventType:    EventTaskStepEntered,
		Title:        fmt.Sprintf("Task moved to %s", orDefault(stepName, "a new step")),
		Body:         fmt.Sprintf("\"%s\" entered %s.", event.TaskTitle, orDefault(stepName, "a new step")),
		Payload:      payload,
	})
}

// HandleTaskCompleted notifies subscribers of task.completed.
func (s *Service) HandleTaskCompleted(ctx context.Context, event TaskCompleted, lookups SlackLookups) {
	if event.TaskID == "" {
		return
	}
	payload := map[string]string{
		providers.SlackPayloadWorkspaceID:   event.WorkspaceID,
		providers.SlackPayloadWorkspaceName: s.workspaceName(ctx, event.WorkspaceID),
		providers.SlackPayloadStepName:      lookupName(ctx, lookups.StepName, event.StepID),
		providers.SlackPayloadTaskTitle:     event.TaskTitle,
	}
	s.deliverTaskEvent(ctx, event.WorkspaceID, notificationPayload{
		TaskID:       event.TaskID,
		OccurrenceID: "completed:" + event.TaskID + ":" + event.CompletedAt,
		EventType:    EventTaskCompleted,
		Title:        "Task completed",
		Body:         fmt.Sprintf("\"%s\" is completed.", event.TaskTitle),
		Payload:      payload,
	})
}

// HandleClarificationRequestedWithQuestion is HandleClarificationRequested
// carrying the question text, which the Slack provider quotes in the post.
// The pending id stays the occurrence id, so the posted message's ts is
// retrievable with SlackThreads().ByOccurrence(EventTaskSessionClarificationAsked, pendingID).
func (s *Service) HandleClarificationRequestedWithQuestion(ctx context.Context, taskID, sessionID, pendingID, question string) {
	var payload map[string]string
	if question != "" {
		payload = map[string]string{providers.SlackPayloadQuestion: question}
	}
	s.handleSemanticOccurrence(ctx, taskID, sessionID, pendingID, EventTaskSessionClarificationAsked, payload)
}

func (s *Service) deliverTaskEvent(ctx context.Context, workspaceID string, message notificationPayload) {
	owner, ok := s.workspaceOwner(ctx, workspaceID)
	if !ok {
		return
	}
	s.deliverOccurrence(ctx, owner, message)
}

func (s *Service) workspaceName(ctx context.Context, workspaceID string) string {
	if workspaceID == "" || s.taskRepo == nil {
		return ""
	}
	workspace, err := s.taskRepo.GetWorkspace(ctx, workspaceID)
	if err != nil || workspace == nil {
		return ""
	}
	return workspace.Name
}

// slackTaskContext is the Slack provider's resolver for events that carry
// only a task id (turn finished, clarification requested).
func (s *Service) slackTaskContext(ctx context.Context, taskID string) (providers.SlackTaskContext, bool) {
	if taskID == "" || s.taskRepo == nil {
		return providers.SlackTaskContext{}, false
	}
	task, err := s.taskRepo.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return providers.SlackTaskContext{}, false
	}
	return providers.SlackTaskContext{
		TaskTitle:     task.Title,
		WorkspaceID:   task.WorkspaceID,
		WorkspaceName: s.workspaceName(ctx, task.WorkspaceID),
	}, true
}

func (s *Service) slackProvider() *providers.SlackProvider {
	provider, _ := s.providers[models.ProviderTypeSlack].(*providers.SlackProvider)
	return provider
}

// ConfigureSlack wires the Slack provider's task resolver, secret revealer
// and post log. It is a no-op when the provider is not registered.
func (s *Service) ConfigureSlack(revealer providers.SlackSecretRevealer) {
	provider := s.slackProvider()
	if provider == nil {
		return
	}
	provider.SetTaskResolver(s.slackTaskContext)
	provider.SetSecretRevealer(revealer)
	provider.SetPostObserver(func(record providers.SlackPostRecord) {
		s.logger.Info("slack notification posted",
			zap.String("event_type", record.EventType),
			zap.String("occurrence_id", record.OccurrenceID),
			zap.String("task_id", record.TaskID),
			zap.String("session_id", record.TaskSessionID),
			zap.String("slack_channel", record.Channel),
			zap.String("slack_ts", record.TS))
	})
}

// SlackThreads returns the index of posted Slack messages, or nil when the
// Slack provider is not registered.
func (s *Service) SlackThreads() *providers.SlackThreadIndex {
	provider := s.slackProvider()
	if provider == nil {
		return nil
	}
	return provider.Threads()
}

func lookupName(ctx context.Context, lookup func(context.Context, string) string, id string) string {
	if lookup == nil || id == "" {
		return ""
	}
	return lookup(ctx, id)
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
