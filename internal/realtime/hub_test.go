package realtime

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHub_PublishNoSubscribers(t *testing.T) {
	h := NewHub()
	// Must not block or panic — the normal case for a room nobody has an
	// open stream to.
	h.Publish(uuid.New(), NewPresenceMessage(uuid.New(), "running"))
}

func TestHub_MultipleSubscribersReceive(t *testing.T) {
	h := NewHub()
	roomID := uuid.New()

	ch1, unsub1 := h.Subscribe(roomID)
	defer unsub1()
	ch2, unsub2 := h.Subscribe(roomID)
	defer unsub2()

	agentID := uuid.New()
	h.Publish(roomID, NewPresenceMessage(agentID, "running"))

	for i, ch := range []<-chan Message{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Kind != KindPresence || got.Presence == nil || got.Presence.AgentID != agentID || got.Presence.Status != "running" {
				t.Fatalf("subscriber %d got %+v, want presence agent=%s status=running", i, got, agentID)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive the publish", i)
		}
	}
}

func TestHub_PublishOnlyReachesItsOwnRoom(t *testing.T) {
	h := NewHub()
	roomA, roomB := uuid.New(), uuid.New()

	chA, unsubA := h.Subscribe(roomA)
	defer unsubA()
	chB, unsubB := h.Subscribe(roomB)
	defer unsubB()

	h.Publish(roomA, NewPresenceMessage(uuid.New(), "running"))

	select {
	case <-chA:
	case <-time.After(time.Second):
		t.Fatal("roomA subscriber did not receive the publish")
	}
	select {
	case got := <-chB:
		t.Fatalf("roomB subscriber received a publish meant for roomA: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHub_UnsubscribeStopsDeliveryAndCleansUp(t *testing.T) {
	h := NewHub()
	roomID := uuid.New()

	ch, unsubscribe := h.Subscribe(roomID)
	unsubscribe()

	// The channel is closed, so a receive returns immediately with ok=false.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected the channel to be closed after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("receiving from an unsubscribed channel blocked")
	}

	// Publishing after the last subscriber leaves must not panic, and the
	// room's entry must be gone, not leaked as an empty map forever.
	h.Publish(roomID, NewPresenceMessage(uuid.New(), "available"))

	h.mu.Lock()
	_, stillTracked := h.subs[roomID]
	h.mu.Unlock()
	if stillTracked {
		t.Fatal("expected the room's entry to be removed once its last subscriber unsubscribed")
	}
}

func TestHub_UnsubscribeIsIdempotent(t *testing.T) {
	h := NewHub()
	_, unsubscribe := h.Subscribe(uuid.New())
	unsubscribe()
	unsubscribe() // must not panic (double-close)
}

func TestHub_SlowSubscriberDoesNotBlockPublish(t *testing.T) {
	h := NewHub()
	roomID := uuid.New()

	slow, unsubSlow := h.Subscribe(roomID)
	defer unsubSlow()
	fast, unsubFast := h.Subscribe(roomID)
	defer unsubFast()

	done := make(chan struct{})
	go func() {
		// Never drained by this test — fills up and then some. If Publish
		// blocked on a full subscriber, this goroutine would hang and the
		// test would time out below.
		for range subscriberBufferSize + 5 {
			h.Publish(roomID, NewPresenceMessage(uuid.New(), "running"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	select {
	case <-fast:
	case <-time.After(time.Second):
		t.Fatal("fast subscriber received nothing")
	}
	_ = slow
}

func TestNewEventMessage(t *testing.T) {
	msg := NewEventMessage(makeTestEnvelope())
	if msg.Kind != KindEvent {
		t.Fatalf("Kind = %q, want %q", msg.Kind, KindEvent)
	}
	if msg.Event == nil {
		t.Fatal("expected Event to be set")
	}
	if msg.Presence != nil {
		t.Fatal("expected Presence to be nil for an event message")
	}
}

func TestNewPresenceMessage(t *testing.T) {
	agentID := uuid.New()
	msg := NewPresenceMessage(agentID, "available")
	if msg.Kind != KindPresence {
		t.Fatalf("Kind = %q, want %q", msg.Kind, KindPresence)
	}
	if msg.Presence == nil || msg.Presence.AgentID != agentID || msg.Presence.Status != "available" {
		t.Fatalf("Presence = %+v, want agent=%s status=available", msg.Presence, agentID)
	}
	if msg.Event != nil {
		t.Fatal("expected Event to be nil for a presence message")
	}
}
