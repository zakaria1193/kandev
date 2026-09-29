package providers

// fork(slack-notify): Slack provider tests against a fake Slack Web API.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testSlackToken = "xoxb-test-token-never-logged"

type fakeSlackCall struct {
	Auth string
	Body map[string]interface{}
}

type fakeSlack struct {
	mu       sync.Mutex
	calls    []fakeSlackCall
	response string
	status   int
}

func newFakeSlack(t *testing.T) (*fakeSlack, *httptest.Server) {
	t.Helper()
	fake := &fakeSlack{status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fake.mu.Lock()
		fake.calls = append(fake.calls, fakeSlackCall{Auth: r.Header.Get("Authorization"), Body: body})
		response, status := fake.response, fake.status
		fake.mu.Unlock()
		if response == "" {
			channel, _ := body["channel"].(string)
			response = `{"ok":true,"channel":"` + strings.TrimPrefix(channel, "#") + `-id","ts":"1700000000.000100"}`
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeSlack) snapshot() []fakeSlackCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeSlackCall(nil), f.calls...)
}

func slackTestConfig() map[string]interface{} {
	return map[string]interface{}{
		"bot_token":       testSlackToken,
		"default_channel": "#kandev",
		"channels": map[string]interface{}{
			"ws-project": "C0PROJECT",
			"Ideas":      "#ideas",
		},
		"base_url": "http://kandev.lan:3040/",
	}
}

func stepMessage(stepName, workspaceID, workspaceName string) Message {
	return Message{
		EventType:    SlackEventStepEntered,
		TaskID:       "task-1",
		OccurrenceID: "step:42",
		UserID:       "user-1",
		Config:       slackTestConfig(),
		Payload: map[string]string{
			SlackPayloadStepName:      stepName,
			SlackPayloadWorkspaceID:   workspaceID,
			SlackPayloadWorkspaceName: workspaceName,
			SlackPayloadTaskTitle:     "Add <login> & signup",
		},
	}
}

func TestSlackRoutesByWorkspaceIDThenNameThenDefault(t *testing.T) {
	fake, server := newFakeSlack(t)
	provider := NewSlackProviderForAPI(server.URL, server.Client())
	cases := []struct {
		workspaceID, workspaceName, want string
	}{
		{"ws-project", "Project", "C0PROJECT"},
		{"ws-ideas", "Ideas", "#ideas"},
		{"ws-other", "Other", "#kandev"},
	}
	for _, tc := range cases {
		if err := provider.Send(context.Background(), stepMessage("Spec", tc.workspaceID, tc.workspaceName)); err != nil {
			t.Fatalf("send %s: %v", tc.workspaceID, err)
		}
	}
	calls := fake.snapshot()
	if len(calls) != len(cases) {
		t.Fatalf("calls = %d, want %d", len(calls), len(cases))
	}
	for i, tc := range cases {
		if got := calls[i].Body["channel"]; got != tc.want {
			t.Errorf("workspace %s routed to %v, want %s", tc.workspaceID, got, tc.want)
		}
	}
}

func TestSlackWithoutAnyMatchingChannelFails(t *testing.T) {
	_, server := newFakeSlack(t)
	provider := NewSlackProviderForAPI(server.URL, server.Client())
	message := stepMessage("Spec", "ws-other", "Other")
	message.Config = map[string]interface{}{
		"bot_token": testSlackToken,
		"channels":  map[string]interface{}{"ws-project": "C0PROJECT"},
	}
	if err := provider.Send(context.Background(), message); !errors.Is(err, errSlackNoChannel) {
		t.Fatalf("err = %v, want errSlackNoChannel", err)
	}
}

func TestSlackPayloadFormatAndAuth(t *testing.T) {
	fake, server := newFakeSlack(t)
	provider := NewSlackProviderForAPI(server.URL, server.Client())
	if err := provider.Send(context.Background(), stepMessage("Human check", "ws-project", "Project")); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := fake.snapshot()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].Auth != "Bearer "+testSlackToken {
		t.Errorf("authorization header = %q", calls[0].Auth)
	}
	want := "*Project · Human check*: Add &lt;login&gt; &amp; signup\n<http://kandev.lan:3040/t/task-1|Open in Kandev>"
	if got := calls[0].Body["text"]; got != want {
		t.Errorf("text = %q\nwant %q", got, want)
	}
	if calls[0].Body["unfurl_links"] != false {
		t.Errorf("unfurl_links = %v, want false", calls[0].Body["unfurl_links"])
	}
	if _, ok := calls[0].Body["thread_ts"]; ok {
		t.Errorf("top-level post must not carry thread_ts")
	}
}

func TestSlackStepNameFilterIsCaseInsensitiveAndOnlyNarrowsStepEvents(t *testing.T) {
	fake, server := newFakeSlack(t)
	provider := NewSlackProviderForAPI(server.URL, server.Client())
	withFilter := func(message Message) Message {
		message.Config["step_names"] = []interface{}{"Spec", " done "}
		return message
	}
	for _, step := range []string{"Ongoing", "DONE", "spec"} {
		if err := provider.Send(context.Background(), withFilter(stepMessage(step, "ws-project", "Project"))); err != nil {
			t.Fatalf("send %s: %v", step, err)
		}
	}
	completed := withFilter(stepMessage("Ongoing", "ws-project", "Project"))
	completed.EventType = SlackEventTaskCompleted
	if err := provider.Send(context.Background(), completed); err != nil {
		t.Fatalf("send completed: %v", err)
	}
	calls := fake.snapshot()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3 (DONE, spec, completed)", len(calls))
	}
	if !strings.Contains(calls[2].Body["text"].(string), "*Project · Completed*") {
		t.Errorf("completed text = %q", calls[2].Body["text"])
	}
}

func TestSlackClarificationQuotesQuestionAndRecordsTS(t *testing.T) {
	fake, server := newFakeSlack(t)
	provider := NewSlackProviderForAPI(server.URL, server.Client())
	provider.SetTaskResolver(func(_ context.Context, taskID string) (SlackTaskContext, bool) {
		return SlackTaskContext{TaskTitle: "Write spec", WorkspaceID: "ws-project", WorkspaceName: "Project"}, taskID == "task-9"
	})
	var observed []SlackPostRecord
	provider.SetPostObserver(func(record SlackPostRecord) { observed = append(observed, record) })
	message := Message{
		EventType:     slackEventClarification,
		TaskID:        "task-9",
		TaskSessionID: "session-9",
		OccurrenceID:  "pending-9",
		Config:        slackTestConfig(),
		Payload:       map[string]string{SlackPayloadQuestion: "Which DB?\nSQLite or Postgres"},
	}
	if err := provider.Send(context.Background(), message); err != nil {
		t.Fatalf("send: %v", err)
	}
	calls := fake.snapshot()
	want := "*Project · Question*: Write spec\n>Which DB?\n>SQLite or Postgres\n<http://kandev.lan:3040/t/task-9|Open in Kandev>"
	if got := calls[0].Body["text"]; got != want {
		t.Errorf("text = %q\nwant %q", got, want)
	}
	record, ok := provider.Threads().ByOccurrence(slackEventClarification, "pending-9")
	if !ok || record.Channel != "C0PROJECT-id" || record.TS != "1700000000.000100" || record.TaskSessionID != "session-9" {
		t.Fatalf("record = %+v, ok = %v", record, ok)
	}
	if back, ok := provider.Threads().ByThread("C0PROJECT-id", "1700000000.000100"); !ok || back.OccurrenceID != "pending-9" {
		t.Fatalf("thread lookup = %+v, ok = %v", back, ok)
	}
	if len(observed) != 1 || observed[0].TS != record.TS {
		t.Fatalf("observer saw %+v", observed)
	}
}

func TestSlackErrorsNeverContainTheToken(t *testing.T) {
	fake, server := newFakeSlack(t)
	provider := NewSlackProviderForAPI(server.URL, server.Client())
	for _, tc := range []struct {
		status   int
		response string
	}{
		{http.StatusOK, `{"ok":false,"error":"invalid_auth"}`},
		{http.StatusInternalServerError, `oops ` + testSlackToken},
		{http.StatusTooManyRequests, `{}`},
	} {
		fake.mu.Lock()
		fake.status, fake.response = tc.status, tc.response
		fake.mu.Unlock()
		err := provider.Send(context.Background(), stepMessage("Spec", "ws-project", "Project"))
		if err == nil {
			t.Fatalf("status %d: expected an error", tc.status)
		}
		if strings.Contains(err.Error(), testSlackToken) {
			t.Fatalf("error leaks the token: %v", err)
		}
	}
}

func TestSlackTokenSources(t *testing.T) {
	fake, server := newFakeSlack(t)
	provider := NewSlackProviderForAPI(server.URL, server.Client())
	t.Setenv("SLACK_FORK_TEST_TOKEN", "xoxb-from-env")
	provider.SetSecretRevealer(func(_ context.Context, userID, secretID string) (string, error) {
		if userID != "user-1" || secretID != "secret-1" {
			return "", errors.New("not visible")
		}
		return "xoxb-from-secret\n", nil
	})
	send := func(config map[string]interface{}) error {
		message := stepMessage("Spec", "ws-project", "Project")
		message.Config = config
		return provider.Send(context.Background(), message)
	}
	if err := send(map[string]interface{}{"bot_token_env": "SLACK_FORK_TEST_TOKEN", "default_channel": "C1"}); err != nil {
		t.Fatalf("env token: %v", err)
	}
	if err := send(map[string]interface{}{"bot_token_ref": "secret-1", "default_channel": "C1"}); err != nil {
		t.Fatalf("secret token: %v", err)
	}
	if err := send(map[string]interface{}{"bot_token_ref": "someone-elses", "default_channel": "C1"}); err == nil {
		t.Fatal("expected a secret owned by another user to fail")
	}
	calls := fake.snapshot()
	if len(calls) != 2 || calls[0].Auth != "Bearer xoxb-from-env" || calls[1].Auth != "Bearer xoxb-from-secret" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestSlackValidate(t *testing.T) {
	provider := NewSlackProvider()
	valid := []map[string]interface{}{
		{"bot_token_ref": "s", "default_channel": "C1"},
		{"bot_token_env": "KANDEV_SLACK_TOKEN", "channels": map[string]interface{}{"ws": "C1"}},
	}
	for _, config := range valid {
		if err := provider.Validate(config); err != nil {
			t.Errorf("Validate(%v) = %v, want nil", config, err)
		}
	}
	invalid := []map[string]interface{}{
		nil,
		{"default_channel": "C1"},
		{"bot_token": "x"},
		{"bot_token_env": "HOME", "default_channel": "C1"},
		{"bot_token": "x", "default_channel": "C1", "channels": "nope"},
		{"bot_token": "x", "default_channel": "C1", "step_names": "Spec"},
		{"bot_token": "x", "default_channel": "C1", "base_url": "kandev.lan"},
	}
	for _, config := range invalid {
		if err := provider.Validate(config); err == nil {
			t.Errorf("Validate(%v) = nil, want an error", config)
		}
	}
}

func TestSlackThreadIndexEvictsOldest(t *testing.T) {
	index := NewSlackThreadIndex(2)
	for _, id := range []string{"a", "b", "c"} {
		index.Add(SlackPostRecord{EventType: "e", OccurrenceID: id, Channel: "C", TS: id})
	}
	if _, ok := index.ByOccurrence("e", "a"); ok {
		t.Fatal("oldest record should be evicted")
	}
	if _, ok := index.ByThread("C", "a"); ok {
		t.Fatal("evicted record must leave the thread map too")
	}
	if record, ok := index.ByThread("C", "c"); !ok || record.OccurrenceID != "c" {
		t.Fatalf("newest record missing: %+v", record)
	}
}
