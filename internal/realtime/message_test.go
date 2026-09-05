package realtime

import (
	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/protocol"
)

func makeTestEnvelope() protocol.Envelope {
	return protocol.NewEnvelope(uuid.New(), protocol.OpTaskCreate, protocol.Participant{AgentID: uuid.New()}, map[string]any{"objective": "test"})
}
