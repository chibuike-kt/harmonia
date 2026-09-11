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
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// fakeProviderAgent substitutes for a real anthropic/openai client in
// tests, via Orchestrator's own newProviderClient seam — no real network
// call, no dependency on a provider API key being available in CI.
type fakeProviderAgent struct {
	content     string
	err         error
	shouldPanic bool
	// beforeReturn, if set, runs synchronously inside Generate before it
	// returns — the deterministic way to simulate "something else
	// happens concurrently while a slow generation call is in flight"
	// (see TestIntegration_TitleGenerator_RaceGuardSkipsManualRename)
	// without relying on a real sleep/timing race.
	beforeReturn func()
	// capturedRequest records the last GenerateRequest this fake
	// received, so a test can assert on what Orchestrator/TitleGenerator
	// actually assembled (e.g. custom_instructions prepended into
	// SystemPrompt) rather than only on the reply that came back.
	capturedRequest *provider.GenerateRequest
	// toolCalls, if set, is returned alongside content on every call —
	// unconditionally, regardless of what Tools the request actually
	// declared. That's deliberate for the cascading-disabled proof: it
	// lets a test assert the orchestrator itself is what suppresses a
	// cascade when a room hasn't opted in, not merely that the fake
	// cooperated by staying quiet.
	toolCalls []provider.ToolCall
	// pickupClassifyAs, if set, makes this fake answer ADR-007 batch B's
	// own phase-1 classify_message tool call distinctly from its
	// ordinary content/toolCalls above — needed so one fake client can
	// serve both a phase-1 classification request (recognized by
	// classify_message being one of req.Tools) and, for whichever agent
	// actually wins phase 2's claim, a real phase-2 reply request, each
	// with the response appropriate to what was actually asked.
	pickupClassifyAs *bool
	// pickupUsage is the fixed (input, output) token pair returned
	// alongside pickupClassifyAs's tool call — real-looking usage a test
	// can assert RecordPickupEvaluationUsage actually persisted.
	pickupUsage [2]int
}

func (f *fakeProviderAgent) Generate(_ context.Context, req provider.GenerateRequest) (provider.GenerateResponse, error) {
	if f.capturedRequest != nil {
		*f.capturedRequest = req
	}
	if f.beforeReturn != nil {
		f.beforeReturn()
	}
	if f.shouldPanic {
		panic("fakeProviderAgent: simulated panic")
	}
	if f.err != nil {
		return provider.GenerateResponse{}, f.err
	}
	if f.pickupClassifyAs != nil {
		for _, t := range req.Tools {
			if t.Name == pickupClassifyToolName {
				return provider.GenerateResponse{
					ToolCalls:    []provider.ToolCall{{Name: pickupClassifyToolName, Input: map[string]any{"needs_response": *f.pickupClassifyAs}}},
					InputTokens:  f.pickupUsage[0],
					OutputTokens: f.pickupUsage[1],
				}, nil
			}
		}
	}
	return provider.GenerateResponse{Content: f.content, ToolCalls: f.toolCalls}, nil
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

func doListMessages(h http.HandlerFunc, u user.User, roomID string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), u), http.MethodGet, "/v1/rooms/"+roomID+"/messages", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("room_id", roomID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doRetryMessage(h http.HandlerFunc, u user.User, roomID, messageID string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(user.NewContext(context.Background(), u), http.MethodPost, "/v1/rooms/"+roomID+"/messages/"+messageID+"/retry", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("room_id", roomID)
	rctx.URLParams.Add("message_id", messageID)
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
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch, titleGen, objectiveGen)

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
	if len(got.MentionedAgentIDs) != 0 {
		t.Fatalf("MentionedAgentIDs = %v, want none", got.MentionedAgentIDs)
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
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch, titleGen, objectiveGen)

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
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch, titleGen, objectiveGen)

	nonexistentID := uuid.New()
	body := fmt.Sprintf(`{"content":"hi","mentioned_agent_ids":[%q]}`, nonexistentID)
	if rec := doCreateMessage(h, owner, rm.ID.String(), body); rec.Code != http.StatusNotFound {
		t.Fatalf("nonexistent agent status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	body = fmt.Sprintf(`{"content":"hi","mentioned_agent_ids":[%q]}`, elsewhere.ID)
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
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Hello — this is the generated reply."}, nil
	}
	// A separate Hub for the title generator, deliberately not rec: this
	// is the room's first message, so the auto-title trigger fires too,
	// and its publish would otherwise land as an unpredictable 5th
	// message in rec's channel, breaking this test's exact 4-message
	// sequence assertions below. A fake provider client keeps it from
	// making a real network call in the background regardless.
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Auto Generated Title"}, nil
	}
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	body := fmt.Sprintf(`{"content":"@Claude can you help?","mentioned_agent_ids":[%q]}`, a.ID)
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

// TestIntegration_CreateHandler_SingleAgentImplicitlyAddressed is
// ADR-004's 2026-09-07 addendum central proof: a room with exactly one
// agent treats an unaddressed message as implicitly meant for it — the
// same full running/reply/available sequence a real @mention produces,
// triggered here with no mentioned_agent_ids in the request at all.
func TestIntegration_CreateHandler_SingleAgentImplicitlyAddressed(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-implicit-")
	rm, err := rooms.Create(ctx, &owner.ID, "implicit-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-implicit")
	if err != nil {
		t.Fatalf("register the room's only agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Hi there, no mention needed."}, nil
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Auto Generated Title"}, nil
	}
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	httpRec := doCreateMessage(h, owner, rm.ID.String(), `{"content":"hi, can you help?"}`)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}
	var human Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &human); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(human.MentionedAgentIDs) != 0 {
		t.Fatalf("MentionedAgentIDs = %v, want none — this message never mentioned anyone", human.MentionedAgentIDs)
	}

	first := rec.recv(t)
	if first.Kind != realtime.KindMessage || first.Message == nil || first.Message.ID != human.ID {
		t.Fatalf("first published message = %+v, want the human message %s", first, human.ID)
	}
	running := rec.recv(t)
	if running.Kind != realtime.KindPresence || running.Presence.AgentID != a.ID || running.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("second published message = %+v, want presence running for %s", running, a.ID)
	}
	reply := rec.recv(t)
	if reply.Kind != realtime.KindMessage || reply.Message == nil || reply.Message.AgentID == nil || *reply.Message.AgentID != a.ID {
		t.Fatalf("third published message = %+v, want a reply from the room's only agent", reply)
	}
	if reply.Message.ReplyToMessageID == nil || *reply.Message.ReplyToMessageID != human.ID {
		t.Fatalf("reply.ReplyToMessageID = %v, want %s", reply.Message.ReplyToMessageID, human.ID)
	}
	available := rec.recv(t)
	if available.Kind != realtime.KindPresence || available.Presence.AgentID != a.ID || available.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("fourth published message = %+v, want presence available for %s", available, a.ID)
	}
}

// TestIntegration_CreateHandler_TwoAgentsRequireExplicitMention is the
// addendum's other half: the moment a second agent exists, an
// unaddressed message goes to no one — ambiguity is real again, and
// nothing should be triggered just because it once would have been.
// Proven deterministically, not by sleeping and hoping: the decision of
// whether to call TriggerReply at all happens synchronously inside the
// HTTP handler, before it returns, so by the time doCreateMessage
// returns there either is or is never going to be a second publish —
// the bounded wait below is just how a test observes "nothing else is
// coming," not a race it could flakily win or lose.
func TestIntegration_CreateHandler_TwoAgentsRequireExplicitMention(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-ambiguous-")
	rm, err := rooms.Create(ctx, &owner.ID, "ambiguous-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if _, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-ambiguous-claude"); err != nil {
		t.Fatalf("register first agent: %v", err)
	}
	if _, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-ambiguous-gpt"); err != nil {
		t.Fatalf("register second agent: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(p agent.Provider, _ string) (provider.Agent, error) {
		return nil, fmt.Errorf("no agent should be invoked for an unaddressed message in a %s-provider two-agent room", p)
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Auto Generated Title"}, nil
	}
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	httpRec := doCreateMessage(h, owner, rm.ID.String(), `{"content":"hi, can you help?"}`)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}

	first := rec.recv(t)
	if first.Kind != realtime.KindMessage {
		t.Fatalf("first published message = %+v, want the human message", first)
	}

	select {
	case msg := <-rec.ch:
		t.Fatalf("unexpected second publish = %+v — nothing should have been triggered", msg)
	case <-time.After(300 * time.Millisecond):
		// Nothing else arrived — exactly what an ambiguous, unaddressed
		// message in a two-agent room should produce.
	}

	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored messages = %d, want 1 (the human message only, no auto-reply)", len(stored))
	}
}

// TestIntegration_CreateHandler_PickupDisabledByDefault_DoesNothing is
// ADR-007 batch B's own suppression proof, the same discipline
// TestIntegration_Orchestrator_CascadingDisabledByDefault_MentionInReplyDoesNothing
// already applies to cascading: autonomous_pickup_enabled defaults
// false, and an unaddressed, genuinely actionable-looking message in a
// two-agent room triggers no phase-1 evaluation at all while it's off —
// not because a cooperative fake stays quiet, but because CreateHandler
// itself never calls EvaluateForPickup when the room hasn't opted in.
func TestIntegration_CreateHandler_PickupDisabledByDefault_DoesNothing(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-pickup-disabled-")
	rm, err := rooms.Create(ctx, &owner.ID, "pickup-disabled-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if rm.AutonomousPickupEnabled {
		t.Fatal("expected autonomous_pickup_enabled to default false")
	}
	if _, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-pickup-disabled-1"); err != nil {
		t.Fatalf("register first agent: %v", err)
	}
	if _, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-pickup-disabled-2"); err != nil {
		t.Fatalf("register second agent: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(p agent.Provider, _ string) (provider.Agent, error) {
		return nil, fmt.Errorf("no agent should ever be invoked while autonomous pickup is disabled (provider %s)", p)
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Auto Generated Title"}, nil
	}
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	httpRec := doCreateMessage(h, owner, rm.ID.String(), `{"content":"does anyone want to help with this important task?"}`)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}

	first := rec.recv(t)
	if first.Kind != realtime.KindMessage {
		t.Fatalf("first published message = %+v, want the human message", first)
	}

	select {
	case msg := <-rec.ch:
		t.Fatalf("unexpected second publish = %+v — pickup is disabled, nothing should have been evaluated", msg)
	case <-time.After(300 * time.Millisecond):
	}

	inputTokens, outputTokens, err := s.SumPickupEvaluationUsage(ctx, rm.ID)
	if err != nil {
		t.Fatalf("SumPickupEvaluationUsage: %v", err)
	}
	if inputTokens != 0 || outputTokens != 0 {
		t.Fatalf("SumPickupEvaluationUsage = (%d, %d), want (0, 0) — no evaluation should have run", inputTokens, outputTokens)
	}
}

// TestIntegration_Orchestrator_PickupEnabled_ExactlyOneClaimsAndRepliesWithCostRecorded
// is the build brief's own central proof for batch B: two agents, both
// flagging the same unaddressed message "yes" in phase 1, only one of
// which actually claims it and generates a real reply — and both
// agents' phase-1 spend is captured (build brief item 7), regardless of
// which one won phase 2. The exactly-one-wins guarantee itself is
// proven at higher concurrency, independent of any fake provider, by
// tests/concurrency's own TestIntegration_OnlyOneMessagePickupClaimSucceeds
// — this test proves the end-to-end wiring around that guarantee, not
// the guarantee's own atomicity a second time.
func TestIntegration_Orchestrator_PickupEnabled_ExactlyOneClaimsAndRepliesWithCostRecorded(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-pickup-enabled-")
	rm, err := rooms.Create(ctx, &owner.ID, "pickup-enabled-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	enabled := true
	if _, err := rooms.Update(ctx, rm.ID, nil, nil, nil, &enabled, nil); err != nil {
		t.Fatalf("enable autonomous pickup: %v", err)
	}

	yes1, err := agents.Register(ctx, rm.ID, "Yes1", agent.ProviderAnthropic, nil, "hash-pickup-yes1")
	if err != nil {
		t.Fatalf("register Yes1: %v", err)
	}
	yes2, err := agents.Register(ctx, rm.ID, "Yes2", agent.ProviderOpenAI, nil, "hash-pickup-yes2")
	if err != nil {
		t.Fatalf("register Yes2: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")
	t.Setenv("OPENAI_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)

	trueVal := true
	orch.newProviderClient = func(p agent.Provider, _ string) (provider.Agent, error) {
		switch p {
		case agent.ProviderAnthropic:
			return &fakeProviderAgent{content: "Yes1's real reply", pickupClassifyAs: &trueVal, pickupUsage: [2]int{12, 3}}, nil
		case agent.ProviderOpenAI:
			return &fakeProviderAgent{content: "Yes2's real reply", pickupClassifyAs: &trueVal, pickupUsage: [2]int{15, 4}}, nil
		default:
			return nil, fmt.Errorf("unexpected provider %s", p)
		}
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "does anyone want to help review this?", nil)
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.EvaluateForPickup([]agent.Agent{yes1, yes2}, rm.OwnerID, triggering)

	// 2 phase-1 usage publishes (one per agent) + running/reply/available
	// for whichever one agent's own claim actually won phase 2 — 5
	// messages total, arriving in whatever order two independent
	// goroutines racing against each other happen to complete in.
	var usageMsgs []realtime.PickupUsage
	var winnerReply *realtime.ChatMessage
	sawRunning := map[uuid.UUID]bool{}
	sawAvailable := map[uuid.UUID]bool{}
	for i := 0; i < 5; i++ {
		msg := rec.recv(t)
		switch msg.Kind {
		case realtime.KindPickupUsage:
			usageMsgs = append(usageMsgs, *msg.PickupUsage)
		case realtime.KindPresence:
			if msg.Presence.Status == string(agent.StatusRunning) {
				sawRunning[msg.Presence.AgentID] = true
			} else {
				sawAvailable[msg.Presence.AgentID] = true
			}
		case realtime.KindMessage:
			winnerReply = msg.Message
		default:
			t.Fatalf("unexpected message kind %q", msg.Kind)
		}
	}

	if len(usageMsgs) != 2 {
		t.Fatalf("got %d pickup usage messages, want 2 (one per agent)", len(usageMsgs))
	}
	if winnerReply == nil {
		t.Fatal("no real reply arrived — expected exactly one agent to claim and reply")
	}
	if winnerReply.AgentID == nil {
		t.Fatal("winning reply has no AgentID")
	}
	winnerID := *winnerReply.AgentID
	if winnerID != yes1.ID && winnerID != yes2.ID {
		t.Fatalf("winner %s is neither Yes1 nor Yes2", winnerID)
	}
	if !sawRunning[winnerID] || !sawAvailable[winnerID] {
		t.Fatalf("winner %s missing running/available presence: running=%v available=%v", winnerID, sawRunning[winnerID], sawAvailable[winnerID])
	}
	if len(sawRunning) != 1 || len(sawAvailable) != 1 {
		t.Fatalf("expected exactly one agent to actually run a real generation, got running=%v available=%v", sawRunning, sawAvailable)
	}

	// Cost visibility (build brief item 7): both agents' phase-1 spend
	// is captured regardless of who won phase 2 — the loser's own
	// evaluation was still a real call.
	inputTokens, outputTokens, err := s.SumPickupEvaluationUsage(ctx, rm.ID)
	if err != nil {
		t.Fatalf("SumPickupEvaluationUsage: %v", err)
	}
	if inputTokens != 12+15 || outputTokens != 3+4 {
		t.Fatalf("SumPickupEvaluationUsage = (%d, %d), want (27, 7) — both agents' phase-1 usage", inputTokens, outputTokens)
	}

	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored messages = %d, want 2 (the trigger + exactly one real reply)", len(stored))
	}
}

// TestIntegration_Orchestrator_RoomFramingInSystemPrompt proves ADR-004's
// 2026-09-07 addendum's other half reaches the actual provider call: the
// room's name, its oldest-in-view message as an objective stand-in, its
// other agents, and its human owner's name all appear in the assembled
// SystemPrompt — not just a plain recency window of messages with no
// framing at all.
func TestIntegration_Orchestrator_RoomFramingInSystemPrompt(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "msg-framing-")
	preferredName := "Kingsley"
	if _, err := users.UpdateMe(ctx, owner.ID, nil, &preferredName, nil, nil); err != nil {
		t.Fatalf("set preferred_name: %v", err)
	}

	rm, err := rooms.Create(ctx, &owner.ID, "Widget Planning")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	claude, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-framing-claude")
	if err != nil {
		t.Fatalf("register Claude: %v", err)
	}
	if _, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-framing-gpt"); err != nil {
		t.Fatalf("register GPT: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "reply", capturedRequest: &captured}, nil
	}

	objective, err := s.CreateHuman(ctx, rm.ID, owner.ID, "Let's plan the new widget feature", []uuid.UUID{claude.ID})
	if err != nil {
		t.Fatalf("seed objective message: %v", err)
	}

	orch.TriggerReply(claude.ID, rm.OwnerID, objective, 0)
	waitForReplyMessage(t, ctx, s, rm.ID, objective.ID)

	for _, want := range []string{`"Widget Planning"`, "GPT", "Kingsley", "Let's plan the new widget feature"} {
		if !strings.Contains(captured.SystemPrompt, want) {
			t.Fatalf("SystemPrompt = %q, want it to contain %q", captured.SystemPrompt, want)
		}
	}
}

// TestIntegration_CreateHandler_DuplicateMentionOfSameAgentTriggersOneInvocation
// directly proves ADR-006 batch A's original "a repeated mention
// collapses to one" claim — never backed by a named test the way the
// rest of that report was, unlike this one. This is a genuinely
// different scenario from the duplicate-agent investigation (two
// distinct agent rows that both happen to display as "ChatGPT"): here
// the SAME agent ID appears twice in one request's mentioned_agent_ids,
// which message_mentions' own (message_id, agent_id) primary key would
// reject as a duplicate insert if CreateHandler's dedup didn't collapse
// it first.
func TestIntegration_CreateHandler_DuplicateMentionOfSameAgentTriggersOneInvocation(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-dup-mention-")
	rm, err := rooms.Create(ctx, &owner.ID, "dup-mention-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-dup-mention")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "one reply, not two"}, nil
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Auto Generated Title"}, nil
	}
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	// The same agent ID, twice, in one request — not two different
	// agents that happen to share a display name.
	body := fmt.Sprintf(`{"content":"@Claude @Claude are you there?","mentioned_agent_ids":[%q,%q]}`, a.ID, a.ID)
	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}
	var human Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &human); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(human.MentionedAgentIDs) != 1 {
		t.Fatalf("MentionedAgentIDs = %v, want exactly one entry — the repeated ID collapsed", human.MentionedAgentIDs)
	}

	first := rec.recv(t)
	if first.Kind != realtime.KindMessage || first.Message == nil || first.Message.ID != human.ID {
		t.Fatalf("first published message = %+v, want the human message %s", first, human.ID)
	}
	running := rec.recv(t)
	if running.Kind != realtime.KindPresence || running.Presence.AgentID != a.ID || running.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("second published message = %+v, want presence running for %s", running, a.ID)
	}
	reply := rec.recv(t)
	if reply.Kind != realtime.KindMessage || reply.Message == nil || reply.Message.AgentID == nil || *reply.Message.AgentID != a.ID {
		t.Fatalf("third published message = %+v, want the one reply", reply)
	}
	available := rec.recv(t)
	if available.Kind != realtime.KindPresence || available.Presence.AgentID != a.ID || available.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("fourth published message = %+v, want presence available for %s", available, a.ID)
	}

	// Prove there's no second invocation queued up behind the first,
	// rather than only proving the first one happened.
	select {
	case msg := <-rec.ch:
		t.Fatalf("unexpected fifth publish = %+v — the duplicate mention should never have produced a second invocation", msg)
	case <-time.After(300 * time.Millisecond):
	}

	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored messages = %d, want 2 (the mention plus exactly one reply)", len(stored))
	}
}

// TestIntegration_CreateHandler_MultiMentionTriggersTwoIndependentReplies
// is ADR-006 batch A's central proof: one message mentioning two agents
// produces exactly two independent replies, each running its own full
// invocation (running, generate, publish, available) rather than one
// invocation somehow serving both — and each reply_to_message_id points
// back at the one triggering message.
func TestIntegration_CreateHandler_MultiMentionTriggersTwoIndependentReplies(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-multi-mention-")
	rm, err := rooms.Create(ctx, &owner.ID, "multi-mention-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	claude, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-multi-claude")
	if err != nil {
		t.Fatalf("register first agent: %v", err)
	}
	gpt, err := agents.Register(ctx, rm.ID, "GPT", agent.ProviderOpenAI, nil, "hash-multi-gpt")
	if err != nil {
		t.Fatalf("register second agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")
	t.Setenv("OPENAI_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	// Distinguishes replies by provider, since two real agents mentioned
	// together are a realistic case this fake needs to tell apart — the
	// seam's signature has no agent identity, only the provider being
	// resolved for.
	orch.newProviderClient = func(p agent.Provider, _ string) (provider.Agent, error) {
		switch p {
		case agent.ProviderAnthropic:
			return &fakeProviderAgent{content: "Claude's reply"}, nil
		case agent.ProviderOpenAI:
			return &fakeProviderAgent{content: "GPT's reply"}, nil
		default:
			return nil, fmt.Errorf("unexpected provider %s", p)
		}
	}
	// Separate Hub for the title generator — same reasoning as
	// TestIntegration_CreateHandler_MentionTriggersReply: this is the
	// room's first message, so the auto-title trigger fires too, and its
	// publish would otherwise land as an unpredictable extra message in
	// rec's channel.
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	titleGen.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Auto Generated Title"}, nil
	}
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	body := fmt.Sprintf(`{"content":"@Claude @GPT can one of you look at this?","mentioned_agent_ids":[%q,%q]}`, claude.ID, gpt.ID)
	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}
	var mention Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(mention.MentionedAgentIDs) != 2 {
		t.Fatalf("MentionedAgentIDs = %v, want both agents", mention.MentionedAgentIDs)
	}

	// The human message itself always arrives first — published
	// synchronously, before either agent's invocation goroutine is even
	// launched.
	first := rec.recv(t)
	if first.Kind != realtime.KindMessage || first.Message == nil || first.Message.ID != mention.ID {
		t.Fatalf("first published message = %+v, want the human message %s", first, mention.ID)
	}

	// The two agents' invocations run concurrently, so their six
	// remaining messages (running, reply, available — per agent) can
	// interleave in either order relative to each other. What must hold
	// regardless of interleaving: each agent's own three arrive in that
	// relative order, and both replies point back at the one mention.
	type perAgent struct {
		sawRunning, sawReply, sawAvailable bool
		replyContent                       string
		replyToID                          uuid.UUID
	}
	byAgent := map[uuid.UUID]*perAgent{claude.ID: {}, gpt.ID: {}}

	for i := 0; i < 6; i++ {
		msg := rec.recv(t)
		switch msg.Kind {
		case realtime.KindPresence:
			pa, ok := byAgent[msg.Presence.AgentID]
			if !ok {
				t.Fatalf("presence for unexpected agent %s", msg.Presence.AgentID)
			}
			switch msg.Presence.Status {
			case string(agent.StatusRunning):
				if pa.sawReply || pa.sawAvailable {
					t.Fatalf("agent %s: running arrived out of order", msg.Presence.AgentID)
				}
				pa.sawRunning = true
			case string(agent.StatusAvailable):
				if !pa.sawReply {
					t.Fatalf("agent %s: available arrived before its reply", msg.Presence.AgentID)
				}
				pa.sawAvailable = true
			default:
				t.Fatalf("unexpected presence status %q", msg.Presence.Status)
			}
		case realtime.KindMessage:
			reply := msg.Message
			if reply.AgentID == nil {
				t.Fatalf("agent reply with no AgentID: %+v", reply)
			}
			pa, ok := byAgent[*reply.AgentID]
			if !ok {
				t.Fatalf("reply from unexpected agent %s", *reply.AgentID)
			}
			if !pa.sawRunning || pa.sawReply {
				t.Fatalf("agent %s: reply arrived out of order", *reply.AgentID)
			}
			pa.sawReply = true
			pa.replyContent = reply.Content
			if reply.ReplyToMessageID != nil {
				pa.replyToID = *reply.ReplyToMessageID
			}
		default:
			t.Fatalf("unexpected message kind %q", msg.Kind)
		}
	}

	claudeState, gptState := byAgent[claude.ID], byAgent[gpt.ID]
	if !claudeState.sawRunning || !claudeState.sawReply || !claudeState.sawAvailable {
		t.Fatalf("Claude's invocation incomplete: %+v", claudeState)
	}
	if !gptState.sawRunning || !gptState.sawReply || !gptState.sawAvailable {
		t.Fatalf("GPT's invocation incomplete: %+v", gptState)
	}
	if claudeState.replyContent != "Claude's reply" {
		t.Fatalf("Claude's reply content = %q, want %q", claudeState.replyContent, "Claude's reply")
	}
	if gptState.replyContent != "GPT's reply" {
		t.Fatalf("GPT's reply content = %q, want %q", gptState.replyContent, "GPT's reply")
	}
	if claudeState.replyToID != mention.ID || gptState.replyToID != mention.ID {
		t.Fatalf("replies point at reply_to_message_id = %s / %s, want both = %s", claudeState.replyToID, gptState.replyToID, mention.ID)
	}

	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("stored messages = %d, want 3 (the mention plus two replies)", len(stored))
	}
}

// TestIntegration_Orchestrator_CascadingDisabledByDefault_MentionInReplyDoesNothing
// is ADR-006 batch B's opt-in proof: a room's agent_cascading_enabled
// defaults false, and an agent's reply attempting to mention another
// agent does nothing when it's off — not merely because a cooperative
// fake stays quiet, but because the orchestrator itself never offers the
// mention_agent tool and never acts on a tool call when cascading isn't
// enabled. The fake here returns a tool call unconditionally, regardless
// of what tools the request declared, specifically to prove suppression
// is the orchestrator's own doing.
func TestIntegration_Orchestrator_CascadingDisabledByDefault_MentionInReplyDoesNothing(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "msg-cascade-off-")
	rm, err := rooms.Create(ctx, &owner.ID, "cascade-off-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if rm.AgentCascadingEnabled {
		t.Fatal("expected agent_cascading_enabled to default false")
	}
	ping, err := agents.Register(ctx, rm.ID, "Ping", agent.ProviderAnthropic, nil, "hash-cascade-off-ping")
	if err != nil {
		t.Fatalf("register Ping: %v", err)
	}
	pong, err := agents.Register(ctx, rm.ID, "Pong", agent.ProviderOpenAI, nil, "hash-cascade-off-pong")
	if err != nil {
		t.Fatalf("register Pong: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")
	t.Setenv("OPENAI_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	pongCalled := false
	orch.newProviderClient = func(p agent.Provider, _ string) (provider.Agent, error) {
		switch p {
		case agent.ProviderAnthropic:
			return &fakeProviderAgent{
				content:   "ping went ahead without pong",
				toolCalls: []provider.ToolCall{{Name: mentionAgentToolName, Input: map[string]any{"agent_name": "Pong"}}},
			}, nil
		case agent.ProviderOpenAI:
			pongCalled = true
			return nil, fmt.Errorf("Pong should never be invoked while cascading is disabled")
		default:
			return nil, fmt.Errorf("unexpected provider %s", p)
		}
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Ping go", []uuid.UUID{ping.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(ping.ID, rm.OwnerID, triggering, 0)

	running := rec.recv(t)
	if running.Kind != realtime.KindPresence || running.Presence.AgentID != ping.ID || running.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("first published message = %+v, want presence running for Ping", running)
	}
	reply := rec.recv(t)
	if reply.Kind != realtime.KindMessage || reply.Message == nil || reply.Message.AgentID == nil || *reply.Message.AgentID != ping.ID {
		t.Fatalf("second published message = %+v, want Ping's reply", reply)
	}
	if len(reply.Message.MentionedAgentIDs) != 0 {
		t.Fatalf("Ping's reply MentionedAgentIDs = %v, want none — cascading is off", reply.Message.MentionedAgentIDs)
	}
	available := rec.recv(t)
	if available.Kind != realtime.KindPresence || available.Presence.AgentID != ping.ID || available.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("third published message = %+v, want presence available for Ping", available)
	}

	// Ping's own invoke() has now fully returned (its last action was the
	// available-presence publish just received above) without ever
	// reaching a TriggerReply call for Pong — there is no further
	// goroutine left that could still publish something later, so this
	// is a deterministic end state to assert against, not a race won by
	// waiting long enough.
	if pongCalled {
		t.Fatal("Pong's provider client was invoked — cascading should have suppressed this entirely")
	}
	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored messages = %d, want 2 (the mention plus Ping's reply only)", len(stored))
	}
	finalPong, err := agents.GetByID(ctx, pong.ID)
	if err != nil {
		t.Fatalf("GetByID Pong: %v", err)
	}
	if finalPong.Status != agent.StatusAvailable {
		t.Fatalf("Pong's status = %s, want unchanged %s", finalPong.Status, agent.StatusAvailable)
	}
}

// TestIntegration_Orchestrator_CascadeStopsExactlyAtDepthCap is ADR-006
// batch B's other central proof: two agents configured to always mention
// each other back, with cascading enabled, chain exactly cascadeDepthCap
// hops deep and then stop with a visible message — never a silent
// truncation, and never one hop more or less. Written deterministically,
// not against a timeout: this ping-pong chain is provably sequential
// (each hop's TriggerReply is only called after the previous hop's own
// publish/status-update work has already completed), so the exact
// publish sequence below is the only order this can ever produce, not
// one that merely usually happens.
func TestIntegration_Orchestrator_CascadeStopsExactlyAtDepthCap(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "msg-cascade-cap-")
	rm, err := rooms.Create(ctx, &owner.ID, "cascade-cap-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	enabled := true
	if _, err := rooms.Update(ctx, rm.ID, nil, nil, &enabled, nil, nil); err != nil {
		t.Fatalf("enable cascading: %v", err)
	}
	ping, err := agents.Register(ctx, rm.ID, "Ping", agent.ProviderAnthropic, nil, "hash-cascade-cap-ping")
	if err != nil {
		t.Fatalf("register Ping: %v", err)
	}
	pong, err := agents.Register(ctx, rm.ID, "Pong", agent.ProviderOpenAI, nil, "hash-cascade-cap-pong")
	if err != nil {
		t.Fatalf("register Pong: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")
	t.Setenv("OPENAI_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(p agent.Provider, _ string) (provider.Agent, error) {
		switch p {
		case agent.ProviderAnthropic:
			return &fakeProviderAgent{
				content:   "ping says hi",
				toolCalls: []provider.ToolCall{{Name: mentionAgentToolName, Input: map[string]any{"agent_name": "Pong"}}},
			}, nil
		case agent.ProviderOpenAI:
			return &fakeProviderAgent{
				content:   "pong says hi",
				toolCalls: []provider.ToolCall{{Name: mentionAgentToolName, Input: map[string]any{"agent_name": "Ping"}}},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected provider %s", p)
		}
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Ping go", []uuid.UUID{ping.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(ping.ID, rm.OwnerID, triggering, 0)

	// One hop = presence running, the reply, presence available — for
	// whichever agent is invoked at that hop. hops[i] is the agent
	// expected at depth i (0-indexed: hop 0 is the human-triggered one).
	hops := []agent.Agent{ping, pong, ping, pong}
	var replyIDs []uuid.UUID
	for i, a := range hops {
		running := rec.recv(t)
		if running.Kind != realtime.KindPresence || running.Presence.AgentID != a.ID || running.Presence.Status != string(agent.StatusRunning) {
			t.Fatalf("hop %d: got %+v, want presence running for %s", i, running, a.Name)
		}
		reply := rec.recv(t)
		if reply.Kind != realtime.KindMessage || reply.Message == nil || reply.Message.AgentID == nil || *reply.Message.AgentID != a.ID {
			t.Fatalf("hop %d: got %+v, want a reply from %s", i, reply, a.Name)
		}
		replyIDs = append(replyIDs, reply.Message.ID)
		available := rec.recv(t)
		if available.Kind != realtime.KindPresence || available.Presence.AgentID != a.ID || available.Presence.Status != string(agent.StatusAvailable) {
			t.Fatalf("hop %d: got %+v, want presence available for %s", i, available, a.Name)
		}
	}

	// Hop 4 (depth+1 = 4 > cascadeDepthCap's 3) never actually invokes
	// Ping — no running/available presence for it, just the one visible
	// stop message, attributed to Ping since it's the agent that would
	// have been invoked next.
	stopMsg := rec.recv(t)
	if stopMsg.Kind != realtime.KindMessage || stopMsg.Message == nil || stopMsg.Message.AgentID == nil || *stopMsg.Message.AgentID != ping.ID {
		t.Fatalf("final published message = %+v, want the cascade-stopped message attributed to Ping", stopMsg)
	}
	if stopMsg.Message.ReplyToMessageID == nil || *stopMsg.Message.ReplyToMessageID != replyIDs[len(replyIDs)-1] {
		t.Fatalf("stop message ReplyToMessageID = %v, want %s (Pong's hop-3 reply)", stopMsg.Message.ReplyToMessageID, replyIDs[len(replyIDs)-1])
	}
	if !strings.Contains(stopMsg.Message.Content, "3-hop limit") {
		t.Fatalf("stop message content = %q, want it to name the 3-hop limit", stopMsg.Message.Content)
	}

	// The chain is now provably wound down (see the test's own doc
	// comment) — Ping was never actually invoked a third time, so both
	// agents' final status is whatever their own last real hop left it
	// at: available.
	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 6 {
		t.Fatalf("stored messages = %d, want 6 (the mention, four replies, one stop message)", len(stored))
	}
	for _, a := range []agent.Agent{ping, pong} {
		final, err := agents.GetByID(ctx, a.ID)
		if err != nil {
			t.Fatalf("GetByID %s: %v", a.Name, err)
		}
		if final.Status != agent.StatusAvailable {
			t.Fatalf("%s final status = %s, want %s", a.Name, final.Status, agent.StatusAvailable)
		}
	}
}

// TestIntegration_Orchestrator_BusyAgentRedirectsViaMentionAgent is
// ADR-007 batch A's central proof: an agent invoked while it's already
// running another generation is told so in its own framing and, using
// the exact same mention_agent tool-call mechanism ADR-006 batch B's
// cascading already proved, redirects to a free agent instead of
// answering itself — a real, visible message (its own actual reply,
// not a synthetic one), followed by the redirected agent's own real
// reply, correctly linked by reply_to_message_id. No new mechanism: this
// is cascading, triggered by a different signal.
func TestIntegration_Orchestrator_BusyAgentRedirectsViaMentionAgent(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "msg-busy-redirect-")
	rm, err := rooms.Create(ctx, &owner.ID, "busy-redirect-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	enabled := true
	if _, err := rooms.Update(ctx, rm.ID, nil, nil, &enabled, nil, nil); err != nil {
		t.Fatalf("enable cascading: %v", err)
	}
	busy, err := agents.Register(ctx, rm.ID, "Busy", agent.ProviderAnthropic, nil, "hash-busy-redirect-busy")
	if err != nil {
		t.Fatalf("register Busy: %v", err)
	}
	free, err := agents.Register(ctx, rm.ID, "Free", agent.ProviderOpenAI, nil, "hash-busy-redirect-free")
	if err != nil {
		t.Fatalf("register Free: %v", err)
	}
	// Busy is already mid-generation on something else when the new
	// mention below arrives — the exact precondition ADR-007 batch A's
	// redirect is for.
	if err := agents.SetStatus(ctx, busy.ID, agent.StatusRunning); err != nil {
		t.Fatalf("mark Busy running: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")
	t.Setenv("OPENAI_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var busyCaptured, freeCaptured provider.GenerateRequest
	orch.newProviderClient = func(p agent.Provider, _ string) (provider.Agent, error) {
		switch p {
		case agent.ProviderAnthropic:
			return &fakeProviderAgent{
				content:         "I'm already working on something else, so I'll let @Free take this one.",
				toolCalls:       []provider.ToolCall{{Name: mentionAgentToolName, Input: map[string]any{"agent_name": "Free"}}},
				capturedRequest: &busyCaptured,
			}, nil
		case agent.ProviderOpenAI:
			return &fakeProviderAgent{content: "I've got it from here.", capturedRequest: &freeCaptured}, nil
		default:
			return nil, fmt.Errorf("unexpected provider %s", p)
		}
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Busy can you look at this?", []uuid.UUID{busy.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	orch.TriggerReply(busy.ID, rm.OwnerID, triggering, 0)

	// Busy's own hop: running, its real (redirecting) reply, available.
	busyRunning := rec.recv(t)
	if busyRunning.Kind != realtime.KindPresence || busyRunning.Presence.AgentID != busy.ID || busyRunning.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("first published message = %+v, want presence running for Busy", busyRunning)
	}
	redirectMsg := rec.recv(t)
	if redirectMsg.Kind != realtime.KindMessage || redirectMsg.Message == nil || redirectMsg.Message.AgentID == nil || *redirectMsg.Message.AgentID != busy.ID {
		t.Fatalf("second published message = %+v, want Busy's own redirect reply", redirectMsg)
	}
	if !strings.Contains(redirectMsg.Message.Content, "I'll let @Free take this one") {
		t.Fatalf("redirect message content = %q, want it to visibly narrate the handoff", redirectMsg.Message.Content)
	}
	busyAvailable := rec.recv(t)
	if busyAvailable.Kind != realtime.KindPresence || busyAvailable.Presence.AgentID != busy.ID || busyAvailable.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("third published message = %+v, want presence available for Busy", busyAvailable)
	}

	// Free's own hop, cascaded from Busy's redirect message.
	freeRunning := rec.recv(t)
	if freeRunning.Kind != realtime.KindPresence || freeRunning.Presence.AgentID != free.ID || freeRunning.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("fourth published message = %+v, want presence running for Free", freeRunning)
	}
	freeReply := rec.recv(t)
	if freeReply.Kind != realtime.KindMessage || freeReply.Message == nil || freeReply.Message.AgentID == nil || *freeReply.Message.AgentID != free.ID {
		t.Fatalf("fifth published message = %+v, want Free's real reply", freeReply)
	}
	if freeReply.Message.ReplyToMessageID == nil || *freeReply.Message.ReplyToMessageID != redirectMsg.Message.ID {
		t.Fatalf("Free's reply.ReplyToMessageID = %v, want %s (Busy's own redirect message)", freeReply.Message.ReplyToMessageID, redirectMsg.Message.ID)
	}
	freeAvailable := rec.recv(t)
	if freeAvailable.Kind != realtime.KindPresence || freeAvailable.Presence.AgentID != free.ID || freeAvailable.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("sixth published message = %+v, want presence available for Free", freeAvailable)
	}

	// The framing signal itself: Busy was told it's busy and may
	// redirect; Free — invoked fresh, never busy — was never told that,
	// so it has no reason to redirect a second time.
	if !strings.Contains(busyCaptured.SystemPrompt, "already generating a reply to something else") {
		t.Fatalf("Busy's SystemPrompt = %q, want it to contain the busy-redirect hint", busyCaptured.SystemPrompt)
	}
	if strings.Contains(freeCaptured.SystemPrompt, "already generating a reply to something else") {
		t.Fatalf("Free's SystemPrompt = %q, want no busy-redirect hint — Free was never busy", freeCaptured.SystemPrompt)
	}
}

// TestIntegration_Orchestrator_CustomInstructionsPrependedToSystemPrompt
// proves ADR-005's custom_instructions field actually reaches the
// provider call, not just the database: the room owner's stored
// instructions are prepended into the GenerateRequest's SystemPrompt
// the same way buildGenerateRequest does for every real reply.
func TestIntegration_Orchestrator_CustomInstructionsPrependedToSystemPrompt(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)

	owner := seedMessageTestUser(t, ctx, users, "msg-custom-instr-")
	instructions := "Always answer in Spanish."
	if _, err := users.UpdateMe(ctx, owner.ID, nil, nil, nil, &instructions); err != nil {
		t.Fatalf("set custom_instructions: %v", err)
	}

	rm, err := rooms.Create(ctx, &owner.ID, "custom-instr-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-custom-instr")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "reply", capturedRequest: &captured}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude hi", []uuid.UUID{a.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}

	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0)
	waitForReplyMessage(t, ctx, s, rm.ID, triggering.ID)

	if !strings.Contains(captured.SystemPrompt, instructions) {
		t.Fatalf("SystemPrompt = %q, want it to contain the owner's custom instructions %q", captured.SystemPrompt, instructions)
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
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{err: errors.New("simulated provider error: rate limited")}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Flaky are you there?", []uuid.UUID{a.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}

	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0)

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
	beginner := store.PoolBeginner{Pool: pool}
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{shouldPanic: true}, nil
	}

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Unstable go", []uuid.UUID{a.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}

	// If the panic weren't recovered, this test binary itself would
	// crash here — the test passing at all is part of the proof, not
	// just the assertions below.
	orch.TriggerReply(a.ID, rm.OwnerID, triggering, 0)

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

	sumPickupUsage := func(ctx context.Context, roomID uuid.UUID) (int, int, error) {
		return s.SumPickupEvaluationUsage(ctx, roomID)
	}

	hub := realtime.NewHub()
	streamHandler := realtime.StreamHandler(rooms, agents, events, listMessages, sumPickupUsage, hub, rdb)

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

// TestIntegration_RetryHandler_TriggersFreshReply proves retry re-runs a
// full invocation against the same triggering message an existing agent
// reply already answered — a second, independent reply, not a mutation
// of the first, with the same running/reply/available publish sequence
// any other invocation produces.
func TestIntegration_RetryHandler_TriggersFreshReply(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-retry-")
	rm, err := rooms.Create(ctx, &owner.ID, "retry-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-retry")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "the retried reply"}, nil
	}
	h := s.RetryHandler(rooms, orch)

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude can you help?", []uuid.UUID{a.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	firstReply, err := s.CreateAgent(ctx, rm.ID, a.ID, "the original, unsatisfying reply", triggering.ID, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed original reply: %v", err)
	}

	httpRec := doRetryMessage(h, owner, rm.ID.String(), firstReply.ID.String())
	if httpRec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusNoContent, httpRec.Body.String())
	}

	running := rec.recv(t)
	if running.Kind != realtime.KindPresence || running.Presence.AgentID != a.ID || running.Presence.Status != string(agent.StatusRunning) {
		t.Fatalf("first published message = %+v, want presence running for %s", running, a.ID)
	}
	reply := rec.recv(t)
	if reply.Kind != realtime.KindMessage || reply.Message == nil {
		t.Fatalf("second published message = %+v, want the retried reply", reply)
	}
	if reply.Message.ID == firstReply.ID {
		t.Fatal("retried reply reused the original message's ID — it should be a new, independent message")
	}
	if reply.Message.Content != "the retried reply" {
		t.Fatalf("retried reply content = %q, want %q", reply.Message.Content, "the retried reply")
	}
	if reply.Message.ReplyToMessageID == nil || *reply.Message.ReplyToMessageID != triggering.ID {
		t.Fatalf("retried reply.ReplyToMessageID = %v, want %s (the original triggering message)", reply.Message.ReplyToMessageID, triggering.ID)
	}
	available := rec.recv(t)
	if available.Kind != realtime.KindPresence || available.Presence.AgentID != a.ID || available.Presence.Status != string(agent.StatusAvailable) {
		t.Fatalf("third published message = %+v, want presence available for %s", available, a.ID)
	}

	stored, err := s.ListByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("ListByRoom: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("stored messages = %d, want 3 (the trigger, the original reply, and the retried reply)", len(stored))
	}
}

// TestIntegration_RetryHandler_RejectsNonAgentMessage proves retry is only
// ever offered for an agent's own reply — there's nothing to regenerate
// for a human's own message.
func TestIntegration_RetryHandler_RejectsNonAgentMessage(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-retry-non-agent-")
	rm, err := rooms.Create(ctx, &owner.ID, "retry-non-agent-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	h := s.RetryHandler(rooms, orch)

	human, err := s.CreateHuman(ctx, rm.ID, owner.ID, "just a note", nil)
	if err != nil {
		t.Fatalf("seed human message: %v", err)
	}

	rec := doRetryMessage(h, owner, rm.ID.String(), human.ID.String())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestIntegration_RetryHandler_RoomOwnership exercises the same
// 404-then-403 ownership pattern every other room-scoped route uses.
func TestIntegration_RetryHandler_RoomOwnership(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-retry-ownership-owner-")
	other := seedMessageTestUser(t, ctx, users, "msg-retry-ownership-other-")
	rm, err := rooms.Create(ctx, &owner.ID, "retry-ownership-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-retry-ownership")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	h := s.RetryHandler(rooms, orch)

	triggering, err := s.CreateHuman(ctx, rm.ID, owner.ID, "@Claude go", []uuid.UUID{a.ID})
	if err != nil {
		t.Fatalf("seed triggering message: %v", err)
	}
	reply, err := s.CreateAgent(ctx, rm.ID, a.ID, "a reply", triggering.ID, nil, nil, nil)
	if err != nil {
		t.Fatalf("seed reply: %v", err)
	}

	if rec := doRetryMessage(h, owner, uuid.New().String(), reply.ID.String()); rec.Code != http.StatusNotFound {
		t.Fatalf("nonexistent room status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if rec := doRetryMessage(h, other, rm.ID.String(), reply.ID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

// TestIntegration_ListByRoomHandler_ReturnsStoredMessages proves the
// artifacts page's cross-room data source: every message in a room, in
// the same shape CreateHandler itself returns, with the same ownership
// check every other room-scoped route uses.
func TestIntegration_ListByRoomHandler_ReturnsStoredMessages(t *testing.T) {
	pool, _ := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)

	owner := seedMessageTestUser(t, ctx, users, "msg-list-")
	other := seedMessageTestUser(t, ctx, users, "msg-list-other-")
	rm, err := rooms.Create(ctx, &owner.ID, "list-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	seeded, err := s.CreateHuman(ctx, rm.ID, owner.ID, "a message with a snippet", nil)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}

	h := s.ListByRoomHandler(rooms)

	rec := doListMessages(h, owner, rm.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got []Message
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0].ID != seeded.ID {
		t.Fatalf("got = %+v, want exactly the one seeded message %s", got, seeded.ID)
	}

	if rec := doListMessages(h, owner, uuid.New().String()); rec.Code != http.StatusNotFound {
		t.Fatalf("nonexistent room status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if rec := doListMessages(h, other, rm.ID.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
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
