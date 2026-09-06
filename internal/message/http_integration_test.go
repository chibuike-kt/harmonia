package message

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// fakeProviderAgent substitutes for a real anthropic/openai client in
// tests, via Orchestrator's own newProviderClient seam — no real network
// call, no dependency on a provider API key being available in CI.
type fakeProviderAgent struct {
	content     string
	err         error
	shouldPanic bool
}

func (f *fakeProviderAgent) Generate(context.Context, provider.GenerateRequest) (provider.GenerateResponse, error) {
	if f.shouldPanic {
		panic("fakeProviderAgent: simulated panic")
	}
	if f.err != nil {
		return provider.GenerateResponse{}, f.err
	}
	return provider.GenerateResponse{Content: f.content}, nil
}

// connectMessageTestPool connects to real Postgres and Redis, skipping
// the test if either isn't configured — run via `make test-integration`
// after `make up`.
func connectMessageTestPool(t *testing.T) (*pgxpool.Pool, *redis.Client) {
	t.Helper()
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping integration test")
	}
	redisAddr := os.Getenv("HARMONIA_REDIS_ADDR")
	if redisAddr == "" {
		t.Skip("HARMONIA_REDIS_ADDR not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	return pool, rdb
}

func seedMessageTestUser(t *testing.T, ctx context.Context, users *user.Store, githubIDPrefix string) user.User {
	t.Helper()
	u, err := users.UpsertByGitHubID(ctx, githubIDPrefix+uuid.New().String(), githubIDPrefix+"user", nil, nil, nil)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// recordingHub is a realtime.Publisher that also records every message
// published to it, so a test can assert on the exact sequence of
// publishes an invocation produces — not just its end state.
type recordingHub struct {
	hub *realtime.Hub
	ch  <-chan realtime.Message
}

func newRecordingHub(roomID uuid.UUID) *recordingHub {
	hub := realtime.NewHub()
	ch, _ := hub.Subscribe(roomID)
	return &recordingHub{hub: hub, ch: ch}
}

func (r *recordingHub) Publish(roomID uuid.UUID, msg realtime.Message) {
	r.hub.Publish(roomID, msg)
}

func (r *recordingHub) recv(t *testing.T) realtime.Message {
	t.Helper()
	select {
	case msg := <-r.ch:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a published message")
		return realtime.Message{}
	}
}

func doCreateMessage(h http.HandlerFunc, u user.User, roomID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), u), http.MethodPost, "/v1/rooms/"+roomID+"/messages", strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("room_id", roomID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestIntegration_CreateHandler_HumanOnly exercises POST
// /v1/rooms/{room_id}/messages with no mention: the message is created
// and published, and no agent is ever touched.
func TestIntegration_CreateHandler_HumanOnly(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-human-only-")
	rm, err := rooms.Create(ctx, &owner.ID, "human-only-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, realtime.NewHub(), rdb)
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch)

	rec := doCreateMessage(h, owner, rm.ID.String(), `{"content":"just a note, no mention"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var got Message
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.SenderKind != SenderHuman {
		t.Fatalf("SenderKind = %q, want %q", got.SenderKind, SenderHuman)
	}
	if got.UserID == nil || *got.UserID != owner.ID {
		t.Fatalf("UserID = %v, want %s", got.UserID, owner.ID)
	}
	if got.MentionedAgentID != nil {
		t.Fatalf("MentionedAgentID = %v, want nil", got.MentionedAgentID)
	}

	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 1 || stored[0].ID != got.ID {
		t.Fatalf("stored messages = %+v, want exactly the one just created", stored)
	}
}

// TestIntegration_CreateHandler_RoomOwnership exercises the same
// 404-then-403 ownership pattern every other room-scoped route uses.
func TestIntegration_CreateHandler_RoomOwnership(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-ownership-owner-")
	other := seedMessageTestUser(t, ctx, users, "msg-ownership-other-")
	rm, err := rooms.Create(ctx, &owner.ID, "ownership-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, realtime.NewHub(), rdb)
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch)

	if rec := doCreateMessage(h, owner, uuid.New().String(), `{"content":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("nonexistent room status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if rec := doCreateMessage(h, other, rm.ID.String(), `{"content":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

// TestIntegration_CreateHandler_MentionedAgentNotFound exercises the
// same non-leaking 404 an agent that doesn't exist and an agent that
// exists in a different room both get — a caller learns nothing about
// an agent it can't reach either way.
func TestIntegration_CreateHandler_MentionedAgentNotFound(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-mention-404-")
	rm, err := rooms.Create(ctx, &owner.ID, "mention-404-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	otherRoom, err := rooms.Create(ctx, &owner.ID, "other-room")
	if err != nil {
		t.Fatalf("create other room: %v", err)
	}
	elsewhere, err := agents.Register(ctx, otherRoom.ID, "elsewhere", agent.ProviderAnthropic, nil, "hash-elsewhere")
	if err != nil {
		t.Fatalf("register agent in other room: %v", err)
	}

	s := NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, realtime.NewHub(), rdb)
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch)

	nonexistentID := uuid.New()
	body := fmt.Sprintf(`{"content":"hi","mentioned_agent_id":%q}`, nonexistentID)
	if rec := doCreateMessage(h, owner, rm.ID.String(), body); rec.Code != http.StatusNotFound {
		t.Fatalf("nonexistent agent status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	body = fmt.Sprintf(`{"content":"hi","mentioned_agent_id":%q}`, elsewhere.ID)
	if rec := doCreateMessage(h, owner, rm.ID.String(), body); rec.Code != http.StatusNotFound {
		t.Fatalf("agent in another room status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestIntegration_CreateHandler_MentionTriggersReply is the phase's
// central proof, run automatically rather than only by hand: an
// @mention on a real, committed human message asynchronously produces a
// real agent reply, with the full presence/publish sequence ADR-004
// describes — the human message, then running, then the reply (with
// reply_to_message_id set to the mention), then available — using a
// fake provider client (Orchestrator's own test seam) so this runs in
// CI without a real network call or provider API key. A real network
// call is verified separately, by hand, per the build brief.
func TestIntegration_CreateHandler_MentionTriggersReply(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-trigger-")
	rm, err := rooms.Create(ctx, &owner.ID, "trigger-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-trigger")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	// No BYOK credential is connected for owner, so resolveClient falls
	// back to the env-var dev path — set a dummy, non-empty value so
	// that fallback succeeds; the fake client below ignores it entirely.
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Hello — this is the generated reply."}, nil
	}
	h := s.CreateHandler(rooms, agents, beginner, rec, orch)

	body := fmt.Sprintf(`{"content":"@Claude can you help?","mentioned_agent_id":%q}`, a.ID)
	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}
	var mention Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// 1. The human message itself, published after commit.
	first := rec.recv(t)
	if first.Kind != realtime.KindMessage || first.Message == nil || first.Message.ID != mention.ID {
		t.Fatalf("first published message = %+v, want the human message %s", first, mention.ID)
	}

	// 2. Presence: running, published at the start of invocation.
	second := rec.recv(t)
	if second.Kind != realtime.KindPresence || second.Presence == nil || second.Presence.AgentID != a.ID || second.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("second published message = %+v, want presence running for agent %s", second, a.ID)
	}

	// 3. The generated reply, with reply_to_message_id set to the mention.
	third := rec.recv(t)
	if third.Kind != realtime.KindMessage || third.Message == nil {
		t.Fatalf("third published message = %+v, want the generated reply", third)
	}
	reply := third.Message
	if reply.SenderKind != string(SenderAgent) || reply.AgentID == nil || *reply.AgentID != a.ID {
		t.Fatalf("reply = %+v, want sender_kind=agent, agent_id=%s", reply, a.ID)
	}
	if reply.ReplyToMessageID == nil || *reply.ReplyToMessageID != mention.ID {
		t.Fatalf("reply.ReplyToMessageID = %v, want %s", reply.ReplyToMessageID, mention.ID)
	}
	if reply.Content != "Hello — this is the generated reply." {
		t.Fatalf("reply.Content = %q, want the fake provider's canned content", reply.Content)
	}

	// 4. Presence: available, published once the invocation is done.
	fourth := rec.recv(t)
	if fourth.Kind != realtime.KindPresence || fourth.Presence == nil || fourth.Presence.AgentID != a.ID || fourth.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("fourth published message = %+v, want presence available for agent %s", fourth, a.ID)
	}

	final, err := agents.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if final.Status != agent.StatusAvailable {
		t.Fatalf("final agent status = %s, want %s", final.Status, agent.StatusAvailable)
	}
}

// TestIntegration_Orchestrator_ProviderErrorProducesVisibleFailureMessage
// exercises ADR-004's "failures are visible messages, never silent"
// requirement directly against the orchestrator: a provider error still
// produces a real, stored, published message explaining what happened,
// and still resets the agent's status.
func TestIntegration_Orchestrator_ProviderErrorProducesVisibleFailureMessage(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "msg-fail-")
	rm, err := rooms.Create(ctx, &owner.ID, "fail-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Flaky", agent.ProviderOpenAI, nil, "hash-fail")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{err: errors.New("simulated provider error: rate limited")}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Flaky are you there?", &a.ID)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}

	orch.TriggerReply(a.ID, rm.OwnerID, triggering)

	// Poll for the failure message itself, not agent status: 'available'
	// is also the agent's status before the goroutine ever runs (set at
	// Register), so waiting on status here would risk a false-positive
	// "done" on the very first check, exactly the race that made this
	// test flaky before the message-based wait replaced it.
	failure := waitForReplyMessage(t, ctx, s, rm.ID, triggering.ID)
	if !strings.Contains(failure.Content, "provider call failed") {
		t.Fatalf("failure message content = %q, want it to explain the provider call failed", failure.Content)
	}
	waitForAgentStatus(t, ctx, agents, a.ID, agent.StatusAvailable)
}

// TestIntegration_Orchestrator_PanicIsRecovered proves the specific
// guarantee the build brief calls out by name: a panic inside the
// invocation goroutine is recovered, never crashes the process, and
// still resolves to a visible failure message and the agent's status
// reset to available rather than leaving it stuck showing "running"
// forever.
func TestIntegration_Orchestrator_PanicIsRecovered(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "msg-panic-")
	rm, err := rooms.Create(ctx, &owner.ID, "panic-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Unstable", agent.ProviderOpenAI, nil, "hash-panic")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("OPENAI_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{shouldPanic: true}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Unstable go", &a.ID)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}

	// If the panic weren't recovered, this test binary itself would
	// crash here — the test passing at all is part of the proof, not
	// just the assertions below.
	orch.TriggerReply(a.ID, rm.OwnerID, triggering)

	failure := waitForReplyMessage(t, ctx, s, rm.ID, triggering.ID)
	if !strings.Contains(failure.Content, "went wrong") {
		t.Fatalf("failure message content = %q, want the panic-path explanation", failure.Content)
	}
	waitForAgentStatus(t, ctx, agents, a.ID, agent.StatusAvailable)
}

// TestIntegration_StreamHandler_SnapshotIncludesMessages proves step 4:
// a client connecting to the SSE stream mid-conversation sees message
// history in the initial snapshot, via the same MessageLister adapter
// httpapi.NewRouter wires in production.
func TestIntegration_StreamHandler_SnapshotIncludesMessages(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	events := event.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-snapshot-")
	rm, err := rooms.Create(ctx, &owner.ID, "snapshot-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	seeded, err := s.CreateHuman(ctx, rm.ID, owner.ID, "history should show up in the snapshot", nil)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}

	listMessages := func(ctx context.Context, roomID uuid.UUID) ([]realtime.ChatMessage, error) {
		msgs, err := s.ListByRoom(ctx, roomID)
		if err != nil {
			return nil, err
		}
		out := make([]realtime.ChatMessage, len(msgs))
		for i, m := range msgs {
			out[i] = toChatMessage(m)
		}
		return out, nil
	}

	hub := realtime.NewHub()
	streamHandler := realtime.StreamHandler(rooms, agents, events, listMessages, hub, rdb)

	sessionPlaintext, sessionHash, err := user.GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if _, err := users.CreateSession(ctx, owner.ID, sessionHash, nil, nil); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	withRoomIDParam := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("room_id", rm.ID.String())
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)))
		}
	}

	srv := httptest.NewServer(user.Authenticate(users)(withRoomIDParam(streamHandler)))
	defer srv.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/rooms/"+rm.ID.String()+"/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: user.SessionCookieName, Value: sessionPlaintext})

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil { // "event: snapshot"
		t.Fatalf("read event line: %v", err)
	}
	dataLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read data line: %v", err)
	}
	rawData := strings.TrimPrefix(strings.TrimSuffix(dataLine, "\n"), "data: ")

	var got struct {
		Messages []realtime.ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(rawData), &got); err != nil {
		t.Fatalf("decode snapshot: %v, raw = %s", err, rawData)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != seeded.ID {
		t.Fatalf("snapshot Messages = %+v, want exactly the seeded message %s", got.Messages, seeded.ID)
	}
}

func findReplyTo(msgs []Message, replyToID uuid.UUID) *Message {
	for i := range msgs {
		if msgs[i].ReplyToMessageID != nil && *msgs[i].ReplyToMessageID == replyToID {
			return &msgs[i]
		}
	}
	return nil
}

// waitForReplyMessage polls ListByRoom until a message replying to
// replyToID appears. Polling for this specific message, not agent
// status, is deliberate: 'available' is also the agent's status before
// the invocation goroutine ever runs, so a status-based wait can return
// on its very first check without the goroutine having done anything —
// this asserts on the one signal that can't already be true beforehand.
func waitForReplyMessage(t *testing.T, ctx context.Context, s *Store, roomID, replyToID uuid.UUID) Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		msgs, err := s.ListByRoom(ctx, roomID)
		if err != nil {
			t.Fatalf("ListByRoom: %v", err)
		}
		if found := findReplyTo(msgs, replyToID); found != nil {
			return *found
		}
		if time.Now().After(deadline) {
			t.Fatalf("no message replying to %s appeared within 10s", replyToID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForAgentStatus(t *testing.T, ctx context.Context, agents *agent.Store, agentID uuid.UUID, want agent.Status) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		a, err := agents.GetByID(ctx, agentID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if a.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent status did not reach %s within 10s; still %s", want, a.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
