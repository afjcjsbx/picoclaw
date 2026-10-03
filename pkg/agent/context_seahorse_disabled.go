//go:build no_seahorse

package agent

import (
	"encoding/json"
	"fmt"
)

func newSeahorseContextManager(_ json.RawMessage, _ *AgentLoop) (ContextManager, error) {
	return nil, fmt.Errorf("seahorse context manager is not included in this build (remove the no_seahorse build tag to enable it)")
}

func init() {
	if err := RegisterContextManager("seahorse", newSeahorseContextManager); err != nil {
		panic(fmt.Sprintf("register seahorse context manager: %v", err))
	}
}
