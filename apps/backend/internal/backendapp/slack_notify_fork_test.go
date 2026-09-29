package backendapp

// fork(slack-notify): event parsing for the step-change notifications.

import (
	"encoding/json"
	"testing"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
)

func taskUpdatedEvent(transitionID interface{}) *bus.Event {
	return bus.NewEvent(events.TaskUpdated, "test", map[string]interface{}{
		"task_id":            "task-1",
		"title":              "Build it",
		"workspace_id":       "ws-1",
		"workflow_id":        "wf-1",
		"workflow_step_id":   "step-2",
		"step_transition_id": transitionID,
	})
}

func TestTaskStepEnteredFromEventAcceptsBusAndJSONNumbers(t *testing.T) {
	for _, id := range []interface{}{int64(12), 12, float64(12), json.Number("12")} {
		got, ok := taskStepEnteredFromEvent(taskUpdatedEvent(id))
		if !ok || got.TransitionID != 12 || got.StepID != "step-2" || got.WorkspaceID != "ws-1" || got.TaskTitle != "Build it" {
			t.Errorf("id %T: got %+v, ok %v", id, got, ok)
		}
	}
}

func TestTaskStepEnteredFromEventIgnoresUpdatesWithoutATransition(t *testing.T) {
	for _, id := range []interface{}{int64(0), nil, "12", float64(-1)} {
		if _, ok := taskStepEnteredFromEvent(taskUpdatedEvent(id)); ok {
			t.Errorf("id %#v: expected no step event", id)
		}
	}
	if _, ok := taskStepEnteredFromEvent(bus.NewEvent(events.TaskUpdated, "test", "not a map")); ok {
		t.Error("non-map payload must be ignored")
	}
	if _, ok := taskStepEnteredFromEvent(nil); ok {
		t.Error("nil event must be ignored")
	}
}

func TestTaskCompletedFromEventOnlyOnTheTransitionIntoCompleted(t *testing.T) {
	event := func(oldState, newState string) *bus.Event {
		return bus.NewEvent(events.TaskStateChanged, "test", map[string]interface{}{
			"task_id": "task-1", "title": "Build it", "workspace_id": "ws-1",
			"workflow_step_id": "step-done", "updated_at": "2026-09-29T10:00:00Z",
			"old_state": oldState, "new_state": newState,
		})
	}
	got, ok := taskCompletedFromEvent(event("IN_PROGRESS", "COMPLETED"))
	if !ok || got.TaskID != "task-1" || got.CompletedAt != "2026-09-29T10:00:00Z" || got.StepID != "step-done" {
		t.Fatalf("got %+v, ok %v", got, ok)
	}
	for _, states := range [][2]string{{"COMPLETED", "COMPLETED"}, {"TODO", "IN_PROGRESS"}, {"", ""}} {
		if _, ok := taskCompletedFromEvent(event(states[0], states[1])); ok {
			t.Errorf("%v: expected no completion event", states)
		}
	}
}

func TestClarificationQuestionText(t *testing.T) {
	single := map[string]interface{}{"content": "Which DB?", "metadata": map[string]interface{}{"question_total": 1}}
	if got := clarificationQuestionText(single); got != "Which DB?" {
		t.Errorf("single = %q", got)
	}
	bundle := map[string]interface{}{"content": "Last one?", "metadata": map[string]interface{}{"question_total": 3}}
	if got := clarificationQuestionText(bundle); got != "Last one?\n(2 more question(s) in Kandev)" {
		t.Errorf("bundle = %q", got)
	}
	if got := clarificationQuestionText(map[string]interface{}{}); got != "" {
		t.Errorf("empty = %q", got)
	}
}
