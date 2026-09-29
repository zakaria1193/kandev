package service

// fork(slack-notify): step-change events and Slack delivery through the
// notification service, against a fake Slack Web API.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/notifications/models"
	"github.com/kandev/kandev/internal/notifications/providers"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const forkSlackToken = "xoxb-service-test-token"

type forkSlackAPI struct {
	mu     sync.Mutex
	bodies []map[string]interface{}
	fail   bool
}

func (a *forkSlackAPI) texts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	texts := make([]string, 0, len(a.bodies))
	for _, body := range a.bodies {
		texts = append(texts, fmt.Sprintf("%v -> %v", body["channel"], body["text"]))
	}
	return texts
}

type forkSlackHarness struct {
	svc  *Service
	repo *notificationTestRepository
	api  *forkSlackAPI
	logs *observer.ObservedLogs
}

func newForkSlackHarness(t *testing.T) *forkSlackHarness {
	t.Helper()
	t.Setenv(desktopNativeNotificationsEnv, "true")
	api := &forkSlackAPI{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		api.mu.Lock()
		api.bodies = append(api.bodies, body)
		fail := api.fail
		count := len(api.bodies)
		api.mu.Unlock()
		if fail {
			_, _ = fmt.Fprint(w, `{"ok":false,"error":"invalid_auth"}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"ok":true,"channel":"%v","ts":"1700000000.%06d"}`, body["channel"], count)
	}))
	t.Cleanup(server.Close)

	core, logs := observer.New(zap.DebugLevel)
	log, err := logger.NewFromZap(zap.New(core))
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{subscriptions: map[string][]*models.Subscription{}}
	tasks := notificationTestTaskGetter{
		task:      &taskmodels.Task{ID: "task-1", Title: "Build the thing", WorkspaceID: "ws-project"},
		workspace: &taskmodels.Workspace{ID: "ws-project", Name: "Project", OwnerID: "user-1"},
	}
	svc := NewService(repo, tasks, nil, log, func() bool { return true })
	svc.providers[models.ProviderTypeSlack] = providers.NewSlackProviderForAPI(server.URL, server.Client())
	svc.ConfigureSlack(nil)
	return &forkSlackHarness{svc: svc, repo: repo, api: api, logs: logs}
}

func (h *forkSlackHarness) seedSlack(t *testing.T, config map[string]interface{}, events ...string) {
	t.Helper()
	if config == nil {
		config = map[string]interface{}{
			"bot_token":       forkSlackToken,
			"default_channel": "#general",
			"channels":        map[string]interface{}{"ws-project": "C0PROJECT"},
		}
	}
	if _, err := h.svc.CreateProvider(context.Background(), "user-1", "Slack", models.ProviderTypeSlack, config, true, events); err != nil {
		t.Fatalf("create slack provider: %v", err)
	}
}

func (h *forkSlackHarness) assertTokenNeverLogged(t *testing.T) {
	t.Helper()
	for _, entry := range h.logs.All() {
		line := entry.Message + fmt.Sprint(entry.ContextMap())
		if strings.Contains(line, forkSlackToken) {
			t.Fatalf("log line leaks the Slack token: %s", line)
		}
	}
}

var forkTestLookups = SlackLookups{
	StepName:     func(_ context.Context, id string) string { return map[string]string{"step-spec": "Spec"}[id] },
	WorkflowName: func(context.Context, string) string { return "Pipeline" },
}

func stepEntered(transitionID int64) TaskStepEntered {
	return TaskStepEntered{
		TaskID: "task-1", TaskTitle: "Build the thing", WorkspaceID: "ws-project",
		WorkflowID: "wf-1", StepID: "step-spec", TransitionID: transitionID,
	}
}

func TestForkEventsHiddenFromAvailableEventsByDefault(t *testing.T) {
	t.Setenv(ForkSlackEventsEnv, "")
	h := newForkSlackHarness(t)
	available := h.svc.AvailableEvents()
	for _, event := range []string{EventTaskStepEntered, EventTaskCompleted} {
		if containsEvent(available, event) {
			t.Errorf("AvailableEvents() = %v, must not list %s with the flag off", available, event)
		}
	}
	h.seedSlack(t, nil, EventTaskStepEntered, EventTaskCompleted)
}

func TestForkEventsAreAvailableAndSubscribable(t *testing.T) {
	t.Setenv(ForkSlackEventsEnv, "true")
	h := newForkSlackHarness(t)
	available := h.svc.AvailableEvents()
	for _, event := range []string{EventTaskStepEntered, EventTaskCompleted, EventTaskSessionClarificationAsked} {
		if !containsEvent(available, event) {
			t.Errorf("AvailableEvents() = %v, missing %s", available, event)
		}
	}
	h.seedSlack(t, nil, EventTaskStepEntered, EventTaskCompleted)
	if _, err := h.svc.CreateProvider(context.Background(), "user-1", "bad", models.ProviderTypeSlack,
		map[string]interface{}{"default_channel": "C1"}, true, nil); err == nil {
		t.Fatal("a Slack provider without a token source must be rejected")
	}
}

func TestStepEnteredPostsOncePerTransitionToTheWorkspaceChannel(t *testing.T) {
	h := newForkSlackHarness(t)
	h.seedSlack(t, nil, EventTaskStepEntered)
	ctx := context.Background()
	h.svc.HandleTaskStepEntered(ctx, stepEntered(7), forkTestLookups)
	h.svc.HandleTaskStepEntered(ctx, stepEntered(7), forkTestLookups) // duplicate task.updated
	h.svc.HandleTaskStepEntered(ctx, stepEntered(8), forkTestLookups)
	h.svc.HandleTaskStepEntered(ctx, stepEntered(0), forkTestLookups) // no transition
	texts := h.api.texts()
	want := "C0PROJECT -> *Project · Spec*: Build the thing"
	if len(texts) != 2 || texts[0] != want || texts[1] != want {
		t.Fatalf("posts = %q, want two of %q", texts, want)
	}
	h.assertTokenNeverLogged(t)
}

func TestStepEventsStayOffUntilSubscribed(t *testing.T) {
	h := newForkSlackHarness(t)
	h.seedSlack(t, nil, EventTaskSessionClarificationAsked)
	h.svc.HandleTaskStepEntered(context.Background(), stepEntered(9), forkTestLookups)
	h.svc.HandleTaskCompleted(context.Background(), TaskCompleted{TaskID: "task-1", WorkspaceID: "ws-project", CompletedAt: "t"}, forkTestLookups)
	if texts := h.api.texts(); len(texts) != 0 {
		t.Fatalf("unsubscribed events were posted: %q", texts)
	}
}

func TestStepFilterNarrowsStepEventsButNotCompletion(t *testing.T) {
	h := newForkSlackHarness(t)
	h.seedSlack(t, map[string]interface{}{
		"bot_token": forkSlackToken, "default_channel": "#general", "step_names": []interface{}{"Human check"},
	}, EventTaskStepEntered, EventTaskCompleted)
	ctx := context.Background()
	h.svc.HandleTaskStepEntered(ctx, stepEntered(10), forkTestLookups) // "Spec" is filtered out
	h.svc.HandleTaskCompleted(ctx, TaskCompleted{TaskID: "task-1", TaskTitle: "Build the thing", WorkspaceID: "ws-project", CompletedAt: "t1"}, forkTestLookups)
	texts := h.api.texts()
	if len(texts) != 1 || texts[0] != "#general -> *Project · Completed*: Build the thing" {
		t.Fatalf("posts = %q", texts)
	}
}

func TestClarificationPostsQuestionAndKeepsTheSlackTS(t *testing.T) {
	h := newForkSlackHarness(t)
	h.seedSlack(t, nil, EventTaskSessionClarificationAsked)
	h.svc.HandleClarificationRequestedWithQuestion(context.Background(), "task-1", "session-1", "pending-1", "Which database?")
	texts := h.api.texts()
	if len(texts) != 1 || texts[0] != "C0PROJECT -> *Project · Question*: Build the thing\n>Which database?" {
		t.Fatalf("posts = %q", texts)
	}
	record, ok := h.svc.SlackThreads().ByOccurrence(EventTaskSessionClarificationAsked, "pending-1")
	if !ok || record.TS != "1700000000.000001" || record.Channel != "C0PROJECT" || record.TaskSessionID != "session-1" {
		t.Fatalf("thread record = %+v, ok = %v", record, ok)
	}
	if h.logs.FilterMessage("slack notification posted").FilterField(zap.String("slack_ts", record.TS)).Len() != 1 {
		t.Fatal("expected the posted ts to be logged")
	}
	h.assertTokenNeverLogged(t)
}

func TestFailedSlackPostIsRetriableAndNeverLogsTheToken(t *testing.T) {
	h := newForkSlackHarness(t)
	h.seedSlack(t, nil, EventTaskStepEntered)
	h.api.mu.Lock()
	h.api.fail = true
	h.api.mu.Unlock()
	h.svc.HandleTaskStepEntered(context.Background(), stepEntered(11), forkTestLookups)
	h.api.mu.Lock()
	h.api.fail = false
	h.api.mu.Unlock()
	h.svc.HandleTaskStepEntered(context.Background(), stepEntered(11), forkTestLookups)
	if texts := h.api.texts(); len(texts) != 2 {
		t.Fatalf("expected the failed delivery to be retried, posts = %q", texts)
	}
	if h.logs.FilterMessage("notification delivery failed").Len() != 1 {
		t.Fatal("expected the failure to be logged once")
	}
	h.assertTokenNeverLogged(t)
}
