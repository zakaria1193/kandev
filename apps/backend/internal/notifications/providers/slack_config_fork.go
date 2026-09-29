package providers

// fork(slack-notify): Slack provider config parsing and message formatting.

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// slackConfig is the parsed provider config:
//
//	{
//	  "bot_token_ref":   "<kandev secret id>",      // preferred
//	  "bot_token_env":   "SLACK_BOT_TOKEN",         // or: env var of the backend
//	  "bot_token":       "xoxb-...",                // or: inline (visible in the API)
//	  "default_channel": "C0123456789" | "#kandev",
//	  "channels":        {"<workspace id or name>": "<channel id or #name>"},
//	  "step_names":      ["Spec", "Human check", "Done"],   // optional filter
//	  "base_url":        "http://192.168.1.10:3040",        // optional task links
//	  "text_prefix":     "[kandev]"                         // optional
//	}
type slackConfig struct {
	botToken       string
	botTokenRef    string
	botTokenEnv    string
	defaultChannel string
	channels       map[string]string
	stepNames      []string
	baseURL        string
	textPrefix     string
}

func parseSlackConfig(config map[string]interface{}) (slackConfig, error) {
	if config == nil {
		return slackConfig{}, errors.New("slack config missing")
	}
	cfg := slackConfig{
		botToken:       configString(config, "bot_token"),
		botTokenRef:    configString(config, "bot_token_ref"),
		botTokenEnv:    configString(config, "bot_token_env"),
		defaultChannel: configString(config, "default_channel"),
		baseURL:        strings.TrimRight(configString(config, "base_url"), "/"),
		textPrefix:     configString(config, "text_prefix"),
	}
	var err error
	if cfg.channels, err = configStringMap(config, "channels"); err != nil {
		return slackConfig{}, err
	}
	if cfg.stepNames, err = configStringList(config, "step_names"); err != nil {
		return slackConfig{}, err
	}
	return cfg, cfg.validate()
}

func (c slackConfig) validate() error {
	if c.botToken == "" && c.botTokenRef == "" && c.botTokenEnv == "" {
		return errors.New("slack: one of bot_token_ref, bot_token_env or bot_token is required")
	}
	if c.botTokenEnv != "" && !strings.HasPrefix(c.botTokenEnv, slackEnvPrefix) && !strings.HasPrefix(c.botTokenEnv, slackKandevEnvPrefix) {
		return fmt.Errorf("slack: bot_token_env must start with %s or %s", slackEnvPrefix, slackKandevEnvPrefix)
	}
	if c.defaultChannel == "" && len(c.channels) == 0 {
		return errors.New("slack: default_channel or channels is required")
	}
	if c.baseURL != "" {
		parsed, err := url.Parse(c.baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("slack: base_url must be an absolute http(s) URL")
		}
	}
	return nil
}

// route picks channels[workspace id], then channels[workspace name], then the
// default channel.
func (c slackConfig) route(workspaceID, workspaceName string) string {
	if channel := c.channels[workspaceID]; workspaceID != "" && channel != "" {
		return channel
	}
	if channel := c.channels[workspaceName]; workspaceName != "" && channel != "" {
		return channel
	}
	return c.defaultChannel
}

// wantsMessage applies the optional step_names filter. It only narrows
// task.step_entered; every other event the provider is subscribed to passes.
func (c slackConfig) wantsMessage(message Message) bool {
	if message.EventType != SlackEventStepEntered || len(c.stepNames) == 0 {
		return true
	}
	step := strings.TrimSpace(message.Payload[SlackPayloadStepName])
	for _, name := range c.stepNames {
		if strings.EqualFold(strings.TrimSpace(name), step) {
			return true
		}
	}
	return false
}

// formatSlackText renders `*<workspace> · <step or event>*: <task title>`,
// the clarification question as a quote, and a link to the task when a base
// URL is configured.
func formatSlackText(message Message, taskCtx SlackTaskContext, cfg slackConfig) string {
	workspace := taskCtx.WorkspaceName
	if workspace == "" {
		workspace = slackFallbackWorkspace
	}
	subject := taskCtx.TaskTitle
	if subject == "" {
		subject = message.Body
	}
	var b strings.Builder
	if cfg.textPrefix != "" {
		b.WriteString(slackEscape(cfg.textPrefix))
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "*%s · %s*: %s", slackEscape(workspace), slackEscape(slackEventLabel(message)), slackEscape(subject))
	if question := strings.TrimSpace(message.Payload[SlackPayloadQuestion]); question != "" {
		for _, line := range strings.Split(question, "\n") {
			b.WriteString("\n>")
			b.WriteString(slackEscape(line))
		}
	}
	if cfg.baseURL != "" && message.TaskID != "" {
		fmt.Fprintf(&b, "\n<%s/t/%s|Open in Kandev>", cfg.baseURL, url.PathEscape(message.TaskID))
	}
	return b.String()
}

func slackEventLabel(message Message) string {
	switch message.EventType {
	case SlackEventStepEntered:
		if step := message.Payload[SlackPayloadStepName]; step != "" {
			return step
		}
		return "Step changed"
	case SlackEventTaskCompleted:
		return "Completed"
	case slackEventClarification:
		if message.TaskID == "" {
			return message.Title
		}
		return "Question"
	case slackEventTurnFinished:
		return "Turn finished"
	default:
		return message.Title
	}
}

// slackEscape escapes the three characters Slack mrkdwn treats as control
// characters, so a task title cannot inject links or mentions.
func slackEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

func configString(config map[string]interface{}, key string) string {
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func configStringMap(config map[string]interface{}, key string) (map[string]string, error) {
	raw, ok := config[key]
	if !ok || raw == nil {
		return map[string]string{}, nil
	}
	result := map[string]string{}
	switch value := raw.(type) {
	case map[string]string:
		for k, v := range value {
			result[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	case map[string]interface{}:
		for k, v := range value {
			text, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("slack: %s.%s must be a string", key, k)
			}
			result[strings.TrimSpace(k)] = strings.TrimSpace(text)
		}
	default:
		return nil, fmt.Errorf("slack: %s must be an object of strings", key)
	}
	return result, nil
}

func configStringList(config map[string]interface{}, key string) ([]string, error) {
	raw, ok := config[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch value := raw.(type) {
	case []string:
		return value, nil
	case []interface{}:
		result := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("slack: %s must be a list of strings", key)
			}
			result = append(result, text)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("slack: %s must be a list of strings", key)
	}
}
