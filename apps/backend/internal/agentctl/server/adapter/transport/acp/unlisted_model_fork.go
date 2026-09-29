package acp

// fork(unlisted-model): see FORK.md. agentctl half of the unlisted-model
// patch. The lifecycle asks for a model the ACP catalog does not list; without
// this, agentctl refuses it locally ("not in the agent's N available models")
// before the agent is asked, so the lifecycle falls back to the provider
// default and the chat shows "The executor could not use the saved model
// selection.". With the flag on, the request goes to the agent, which accepts
// or rejects the id itself. agentctl inherits the flag from the kandev process
// environment. Off by default.

import (
	"os"
	"strconv"
	"strings"
)

// forkUnlistedModelsEnv mirrors lifecycle.ForkUnlistedModelsEnv; agentctl must
// not import the lifecycle package.
const forkUnlistedModelsEnv = "KANDEV_FORK_UNLISTED_MODELS"

func forkUnlistedModelsEnabled() bool {
	on, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(forkUnlistedModelsEnv)))
	return err == nil && on
}
