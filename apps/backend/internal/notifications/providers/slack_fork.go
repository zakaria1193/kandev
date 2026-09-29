package providers

// fork(slack-notify): Slack Web API notification provider.
//
// Posts with chat.postMessage, so the bot token needs only chat:write (plus
// chat:write.public to post into public channels it has not joined). The
// token never appears in a log line, an error or a returned value.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Event types the Slack provider knows how to label. The notification service
// owns the full list; these are repeated here so the provider package keeps no
// dependency on the service package.
const (
	SlackEventStepEntered   = "task.step_entered"
	SlackEventTaskCompleted = "task.completed"
	slackEventClarification = "session.clarification_requested"
	slackEventTurnFinished  = "session.turn_finished"
)

// Payload keys the service fills for task step events and clarifications.
const (
	SlackPayloadWorkspaceID   = "workspace_id"
	SlackPayloadWorkspaceName = "workspace_name"
	SlackPayloadWorkflowName  = "workflow_name"
	SlackPayloadStepName      = "step_name"
	SlackPayloadTaskTitle     = "task_title"
	SlackPayloadQuestion      = "question"
)

const (
	slackDefaultAPIBaseURL = "https://slack.com/api"
	slackSendTimeout       = 10 * time.Second
	slackEnvPrefix         = "SLACK_"
	slackKandevEnvPrefix   = "KANDEV_SLACK_"
	slackFallbackWorkspace = "Kandev"
	slackMaxResponseBytes  = 1 << 20
)

var errSlackNoChannel = errors.New("slack: no channel configured for this workspace and no default_channel")

// SlackTaskContext is what the provider needs to know about the task a
// notification is about: its title and the workspace used for routing.
type SlackTaskContext struct {
	TaskTitle     string
	WorkspaceID   string
	WorkspaceName string
}

// SlackTaskResolver looks up a task's title and workspace. It is optional: the
// provider falls back to the default channel when it is unset or misses.
type SlackTaskResolver func(ctx context.Context, taskID string) (SlackTaskContext, bool)

// SlackSecretRevealer decrypts a Kandev secret on behalf of the provider's
// owner. It must scope the lookup to userID so one user cannot point a
// provider at another user's secret.
type SlackSecretRevealer func(ctx context.Context, userID, secretID string) (string, error)

// SlackPostRequest is one chat.postMessage call. ThreadTS posts a reply in an
// existing thread; it is empty for a new top-level message.
type SlackPostRequest struct {
	Channel  string
	Text     string
	ThreadTS string
}

// SlackPostResult is what Slack returns for a posted message: the channel id
// (Slack resolves "#name" to an id) and the message timestamp, which is the
// thread id replies hang from.
type SlackPostResult struct {
	Channel string
	TS      string
}

// SlackPostRecord links a posted Slack message back to the Kandev occurrence
// it announced. A later Slack-thread plugin uses it to map a thread reply to
// the clarification (pending id) it answers.
type SlackPostRecord struct {
	UserID        string
	EventType     string
	OccurrenceID  string
	TaskID        string
	TaskSessionID string
	Channel       string
	TS            string
	PostedAt      time.Time
}

// SlackProvider sends notifications to Slack channels, one channel per
// workspace with a default fallback.
type SlackProvider struct {
	apiBaseURL string
	client     *http.Client
	threads    *SlackThreadIndex

	mu           sync.RWMutex
	resolveTask  SlackTaskResolver
	revealSecret SlackSecretRevealer
	onPosted     func(SlackPostRecord)
}

// NewSlackProvider returns a provider that talks to the real Slack API.
func NewSlackProvider() *SlackProvider {
	return newSlackProvider(slackDefaultAPIBaseURL, &http.Client{Timeout: slackSendTimeout})
}

// NewSlackProviderForAPI points the provider at another Slack-compatible API
// base URL; tests use it with a fake server. The base URL is deliberately not
// part of the user-editable config: a configurable URL would let a provider
// owner send a revealed token to a host of their choice.
func NewSlackProviderForAPI(apiBaseURL string, client *http.Client) *SlackProvider {
	return newSlackProvider(apiBaseURL, client)
}

func newSlackProvider(apiBaseURL string, client *http.Client) *SlackProvider {
	return &SlackProvider{
		apiBaseURL: strings.TrimRight(apiBaseURL, "/"),
		client:     client,
		threads:    NewSlackThreadIndex(slackThreadIndexCapacity),
	}
}

// SetTaskResolver installs the task/workspace lookup used for routing.
func (p *SlackProvider) SetTaskResolver(resolver SlackTaskResolver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resolveTask = resolver
}

// SetSecretRevealer installs the lookup behind config.bot_token_ref.
func (p *SlackProvider) SetSecretRevealer(revealer SlackSecretRevealer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revealSecret = revealer
}

// SetPostObserver installs a callback run after every successful post.
func (p *SlackProvider) SetPostObserver(observer func(SlackPostRecord)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onPosted = observer
}

// Threads exposes the in-memory index of posted messages.
func (p *SlackProvider) Threads() *SlackThreadIndex {
	return p.threads
}

func (p *SlackProvider) Available() bool {
	return true
}

func (p *SlackProvider) Validate(config map[string]interface{}) error {
	_, err := parseSlackConfig(config)
	return err
}

// Send formats one notification and posts it to the channel its workspace
// routes to. A task step event whose step is not in config.step_names is
// dropped silently: it is filtered, not failed.
func (p *SlackProvider) Send(ctx context.Context, message Message) error {
	cfg, err := parseSlackConfig(message.Config)
	if err != nil {
		return err
	}
	if !cfg.wantsMessage(message) {
		return nil
	}
	taskCtx := p.taskContext(ctx, message)
	channel := cfg.route(taskCtx.WorkspaceID, taskCtx.WorkspaceName)
	if channel == "" {
		return errSlackNoChannel
	}
	token, err := p.botToken(ctx, cfg, message.UserID)
	if err != nil {
		return err
	}
	result, err := p.PostMessage(ctx, token, SlackPostRequest{
		Channel: channel,
		Text:    formatSlackText(message, taskCtx, cfg),
	})
	if err != nil {
		return err
	}
	p.recordPost(message, result)
	return nil
}

func (p *SlackProvider) recordPost(message Message, result SlackPostResult) {
	record := SlackPostRecord{
		UserID:        message.UserID,
		EventType:     message.EventType,
		OccurrenceID:  message.OccurrenceID,
		TaskID:        message.TaskID,
		TaskSessionID: message.TaskSessionID,
		Channel:       result.Channel,
		TS:            result.TS,
		PostedAt:      time.Now().UTC(),
	}
	p.threads.Add(record)
	p.mu.RLock()
	observer := p.onPosted
	p.mu.RUnlock()
	if observer != nil {
		observer(record)
	}
}

// PostMessage is the single call into the Slack Web API. It is exported so a
// thread-reply integration can post follow-ups (ThreadTS) with the same
// client and the same token handling.
func (p *SlackProvider) PostMessage(ctx context.Context, token string, request SlackPostRequest) (SlackPostResult, error) {
	if strings.TrimSpace(token) == "" {
		return SlackPostResult{}, errors.New("slack: bot token is empty")
	}
	body, err := json.Marshal(slackPostBody{
		Channel:     request.Channel,
		Text:        request.Text,
		ThreadTS:    request.ThreadTS,
		UnfurlLinks: false,
	})
	if err != nil {
		return SlackPostResult{}, fmt.Errorf("slack: encode message: %w", err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, slackSendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodPost, p.apiBaseURL+"/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return SlackPostResult{}, fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.client.Do(req)
	if err != nil {
		return SlackPostResult{}, fmt.Errorf("slack: chat.postMessage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeSlackPostResponse(resp)
}

type slackPostBody struct {
	Channel     string `json:"channel"`
	Text        string `json:"text"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	UnfurlLinks bool   `json:"unfurl_links"`
}

type slackPostResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

func decodeSlackPostResponse(resp *http.Response) (SlackPostResult, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, slackMaxResponseBytes))
	if err != nil {
		return SlackPostResult{}, fmt.Errorf("slack: read response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return SlackPostResult{}, fmt.Errorf("slack: rate limited (retry after %s s)", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return SlackPostResult{}, fmt.Errorf("slack: chat.postMessage returned HTTP %d", resp.StatusCode)
	}
	var decoded slackPostResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return SlackPostResult{}, fmt.Errorf("slack: decode response: %w", err)
	}
	if !decoded.OK {
		return SlackPostResult{}, fmt.Errorf("slack: chat.postMessage failed: %s", decoded.Error)
	}
	return SlackPostResult{Channel: decoded.Channel, TS: decoded.TS}, nil
}

func (p *SlackProvider) taskContext(ctx context.Context, message Message) SlackTaskContext {
	taskCtx := SlackTaskContext{
		TaskTitle:     message.Payload[SlackPayloadTaskTitle],
		WorkspaceID:   message.Payload[SlackPayloadWorkspaceID],
		WorkspaceName: message.Payload[SlackPayloadWorkspaceName],
	}
	if message.TaskID == "" || (taskCtx.WorkspaceID != "" && taskCtx.TaskTitle != "") {
		return taskCtx
	}
	p.mu.RLock()
	resolver := p.resolveTask
	p.mu.RUnlock()
	if resolver == nil {
		return taskCtx
	}
	resolved, ok := resolver(ctx, message.TaskID)
	if !ok {
		return taskCtx
	}
	if taskCtx.TaskTitle == "" {
		taskCtx.TaskTitle = resolved.TaskTitle
	}
	if taskCtx.WorkspaceID == "" {
		taskCtx.WorkspaceID = resolved.WorkspaceID
		taskCtx.WorkspaceName = resolved.WorkspaceName
	}
	return taskCtx
}

// botToken resolves the token in order: secret reference, environment
// variable, inline value. Errors name the source, never the value.
func (p *SlackProvider) botToken(ctx context.Context, cfg slackConfig, userID string) (string, error) {
	if cfg.botTokenRef != "" {
		p.mu.RLock()
		reveal := p.revealSecret
		p.mu.RUnlock()
		if reveal == nil {
			return "", errors.New("slack: bot_token_ref is set but no secret store is wired")
		}
		token, err := reveal(ctx, userID, cfg.botTokenRef)
		if err != nil {
			return "", fmt.Errorf("slack: resolve bot_token_ref %q: %w", cfg.botTokenRef, err)
		}
		return strings.TrimSpace(token), nil
	}
	if cfg.botTokenEnv != "" {
		token := strings.TrimSpace(os.Getenv(cfg.botTokenEnv))
		if token == "" {
			return "", fmt.Errorf("slack: environment variable %s is empty", cfg.botTokenEnv)
		}
		return token, nil
	}
	return cfg.botToken, nil
}
