package realtime

import (
	"sync"

	"github.com/google/uuid"
)

// subscriberBufferSize bounds each subscriber's channel so one slow
// consumer can't block Publish for every other subscriber, in this room
// or any other — Publish holds Hub's lock while it fans out, so a
// blocking send there would stall every handler trying to publish
// anywhere. A full buffer drops the message for that subscriber instead;
// a dropped live update is recoverable (the SSE endpoint's initial
// snapshot and eventual reconnect catch it up), a stalled publisher isn't.
const subscriberBufferSize = 16

// Hub is an in-process, thread-safe fan-out point for Messages, keyed by
// room ID. One Hub per server process — see ADR-003 on why this doesn't
// need to be Redis Pub/Sub yet.
type Hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan Message]struct{}
}

// NewHub builds an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[uuid.UUID]map[chan Message]struct{})}
}

// Subscribe registers a new subscriber to roomID and returns a
// receive-only channel of Messages published to that room, plus an
// unsubscribe function. Calling unsubscribe more than once is safe.
// Callers must keep draining the channel (or call unsubscribe) — an
// abandoned, un-unsubscribed channel leaks in Hub's map forever.
func (h *Hub) Subscribe(roomID uuid.UUID) (<-chan Message, func()) {
	ch := make(chan Message, subscriberBufferSize)

	h.mu.Lock()
	if h.subs[roomID] == nil {
		h.subs[roomID] = make(map[chan Message]struct{})
	}
	h.subs[roomID][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if room, ok := h.subs[roomID]; ok {
				delete(room, ch)
				if len(room) == 0 {
					delete(h.subs, roomID)
				}
			}
			close(ch)
		})
	}
	return ch, unsubscribe
}

// Publish fans msg out to every current subscriber of roomID. Publishing
// to a room with no subscribers is a safe, cheap no-op — this is the
// normal case for a room nobody has an open stream to right now.
func (h *Hub) Publish(roomID uuid.UUID, msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[roomID] {
		select {
		case ch <- msg:
		default:
			// Slow subscriber — drop rather than block every other
			// subscriber and every other room's Publish call.
		}
	}
}
