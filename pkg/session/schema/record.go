package schema

import (
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// Record is the JSON snapshot format of a session. It is shared by the
// legacy session store (pkg/session) and the migration path (pkg/memory):
// the field set and the JSON tags are part of the persisted format and
// must stay stable.
type Record struct {
	Key      string              `json:"key"`
	Messages []providers.Message `json:"messages"`
	Summary  string              `json:"summary,omitempty"`
	Created  time.Time           `json:"created"`
	Updated  time.Time           `json:"updated"`
}
