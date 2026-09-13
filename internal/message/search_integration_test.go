package message

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// TestIntegration_CreateHandler_RoomToggle_WebSearchReachesGenerate is
// ADR-008 batch B's first proof: a room with web_search_enabled turned on
// makes it all the way to the real GenerateRequest as WebSearchEnabled —
// advisory (RequireToolCall stays false, the model's own judgment still
// decides whether a given question needs a search), and independent of
// forced search (this request never sets force_search). Also proves
// citations returned by the provider get rendered into the stored
// reply's own content via ApplyCitations, not dropped.
func TestIntegration_CreateHandler_RoomToggle_WebSearchReachesGenerate(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-search-toggle-")
	rm, err := rooms.Create(ctx, &owner.ID, "search-toggle-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	enabled := true
	if _, err := rooms.Update(ctx, rm.ID, nil, nil, nil, nil, &enabled, nil); err != nil {
		t.Fatalf("enable web search: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-search-toggle")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{
			content:         "Rates rose to 5.1% this week.",
			capturedRequest: &captured,
			citations: []provider.Citation{
				{Title: "Rate Report", URL: "https://example.com/rates", AfterText: "5.1% this week."},
			},
		}, nil
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	body := `{"content":"@Claude what are mortgage rates doing this week?","mentioned_agent_ids":["` + a.ID.String() + `"]}`
	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}

	var mention Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	reply := waitForReplyMessage(t, ctx, s, rm.ID, mention.ID)

	if !captured.WebSearchEnabled {
		t.Error("captured GenerateRequest.WebSearchEnabled = false, want true — room toggle should reach Generate")
	}
	if captured.RequireToolCall {
		t.Error("captured GenerateRequest.RequireToolCall = true, want false — the room toggle is advisory, not forced")
	}

	if !strings.Contains(reply.Content, "[1]") {
		t.Errorf("reply.Content = %q, want an inline [1] citation marker", reply.Content)
	}
	if !strings.Contains(reply.Content, "https://example.com/rates") {
		t.Errorf("reply.Content = %q, want the Sources list to carry the real citation URL", reply.Content)
	}
}

// TestIntegration_CreateHandler_ForceSearch_IndependentOfRoomToggle is
// ADR-008 batch B's second, defining proof: a human's per-message
// force_search attach produces a real, cited, search-grounded reply in a
// room whose own web_search_enabled toggle is OFF — the two trigger
// paths are genuinely independent, not one gating the other. Also
// proves the forced-search call restricts Tools to search alone
// (RequireToolCall true, no create_task mixed in) so the model can't
// dodge the human's explicit intent.
func TestIntegration_CreateHandler_ForceSearch_IndependentOfRoomToggle(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-force-search-")
	// Deliberately left at its default — web_search_enabled is OFF for
	// this room; the room toggle plays no part in this test at all.
	rm, err := rooms.Create(ctx, &owner.ID, "force-search-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-force-search")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{
			content:         "The championship game is tonight at 8pm ET.",
			capturedRequest: &captured,
			citations: []provider.Citation{
				{Title: "Schedule", URL: "https://example.com/schedule", AfterText: "tonight at 8pm ET."},
			},
		}, nil
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	body := `{"content":"@Claude when is the game tonight?","mentioned_agent_ids":["` + a.ID.String() + `"],"force_search":true}`
	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}

	var mention Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	reply := waitForReplyMessage(t, ctx, s, rm.ID, mention.ID)

	if !captured.WebSearchEnabled {
		t.Error("captured GenerateRequest.WebSearchEnabled = false, want true — force_search should reach Generate regardless of the room toggle")
	}
	if !captured.RequireToolCall {
		t.Error("captured GenerateRequest.RequireToolCall = false, want true — forced search must force the tool call")
	}
	if len(captured.Tools) != 0 {
		t.Errorf("captured GenerateRequest.Tools = %+v, want empty — forced search must not offer create_task/mention_agent/request_handoff to dodge into", captured.Tools)
	}

	if !strings.Contains(reply.Content, "[1]") {
		t.Errorf("reply.Content = %q, want an inline [1] citation marker", reply.Content)
	}
	if !strings.Contains(reply.Content, "https://example.com/schedule") {
		t.Errorf("reply.Content = %q, want the Sources list to carry the real citation URL", reply.Content)
	}
}

// TestIntegration_CreateHandler_NoSearch_ToolsAndWebSearchUntouched is
// the negative-space proof completing the pair above: an ordinary
// message, in a room with web_search_enabled off and no force_search on
// the request, reaches Generate exactly as it always did — Tools carries
// create_task as usual, WebSearchEnabled stays false.
func TestIntegration_CreateHandler_NoSearch_ToolsAndWebSearchUntouched(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-no-search-")
	rm, err := rooms.Create(ctx, &owner.ID, "no-search-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-no-search")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "test-key-unused-by-fake-client")

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	var captured provider.GenerateRequest
	orch.newProviderClient = func(agent.Provider, string) (provider.Agent, error) {
		return &fakeProviderAgent{content: "Sure, happy to help.", capturedRequest: &captured}, nil
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	body := `{"content":"@Claude can you help me with something?","mentioned_agent_ids":["` + a.ID.String() + `"]}`
	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}

	var mention Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	waitForReplyMessage(t, ctx, s, rm.ID, mention.ID)

	if captured.WebSearchEnabled {
		t.Error("captured GenerateRequest.WebSearchEnabled = true, want false for an ordinary message with no toggle and no force_search")
	}
	if captured.RequireToolCall {
		t.Error("captured GenerateRequest.RequireToolCall = true, want false")
	}
	found := false
	for _, tool := range captured.Tools {
		if tool.Name == createTaskToolName {
			found = true
		}
	}
	if !found {
		t.Errorf("captured GenerateRequest.Tools = %+v, want create_task still offered as always", captured.Tools)
	}
}

// waitForRealReplyMessage is waitForReplyMessage's own polling loop with
// a longer deadline — a live provider call with a real web search
// attached genuinely takes longer than the fake-provider tests above,
// which return instantly.
func waitForRealReplyMessage(t *testing.T, ctx context.Context, s *Store, roomID, replyToID uuid.UUID) Message {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		msgs, err := s.ListByRoom(ctx, roomID)
		if err != nil {
			t.Fatalf("ListByRoom: %v", err)
		}
		if found := findReplyTo(msgs, replyToID); found != nil {
			return *found
		}
		if time.Now().After(deadline) {
			t.Fatalf("no message replying to %s appeared within 30s", replyToID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestIntegration_CreateHandler_RoomToggle_LiveWebSearch is ADR-008
// batch B's mandatory live proof, part one: a real room with
// web_search_enabled on, a real agent backed by the live OpenAI
// Responses API (no BYOK credential configured, so resolveClient falls
// back to OPENAI_API_KEY — the same dev-path every other live provider
// test in this codebase already uses), a real search-worthy question,
// and a real reply with real citations rendered into its stored
// content. No fake provider anywhere in this test.
//
// Skips without OPENAI_API_KEY; incurs a small real cost, including the
// Responses API's own per-search fee, when it runs.
func TestIntegration_CreateHandler_RoomToggle_LiveWebSearch(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set; skipping live web search integration test")
	}
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-live-search-toggle-")
	rm, err := rooms.Create(ctx, &owner.ID, "live-search-toggle-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	enabled := true
	if _, err := rooms.Update(ctx, rm.ID, nil, nil, nil, nil, &enabled, nil); err != nil {
		t.Fatalf("enable web search: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Pong", agent.ProviderOpenAI, nil, "hash-live-search-toggle")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	// The room toggle is deliberately advisory (see GenerateRequest.
	// WebSearchEnabled's own doc comment): create_task is always offered
	// too (ADR-006 batch C), so under plain tool_choice "auto" gpt-4o-mini
	// sometimes resolves an ambiguous "auto" choice toward create_task
	// instead of actually searching and answering — genuine, observed
	// model nondeterminism in this specific tool-competition shape, not a
	// plumbing defect (the forced-search test below proves the identical
	// request/response path deterministically, via RequireToolCall). A
	// bounded retry absorbs that real variance without weakening what
	// this test actually proves: that when the model does exercise its
	// own judgment to search, a real, cited, search-grounded reply comes
	// back out through this exact code path.
	var reply Message
	var lastEmptyReply bool
	for attempt := 0; attempt < 3; attempt++ {
		body := `{"content":"@Pong hey, what is a real, current AI industry headline from today? (attempt ` +
			string(rune('1'+attempt)) + `)","mentioned_agent_ids":["` + a.ID.String() + `"]}`
		httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
		if httpRec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
		}

		var mention Message
		if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		reply = waitForRealReplyMessage(t, ctx, s, rm.ID, mention.ID)
		if reply.Content != "" {
			lastEmptyReply = false
			break
		}
		lastEmptyReply = true
		t.Logf("attempt %d: model called create_task instead of answering directly (known advisory-tool-choice nondeterminism); retrying", attempt+1)
	}
	if lastEmptyReply {
		t.Fatal("expected a real, non-empty reply after 3 attempts — the model consistently chose create_task over answering, which would be worth a closer look")
	}
	if !strings.Contains(reply.Content, "**Sources**") || !strings.Contains(reply.Content, "http") {
		t.Fatalf("reply.Content = %q, want a real citation rendered via ApplyCitations (a Sources list with a real URL)", reply.Content)
	}
	t.Logf("live room-toggle search reply: %q", reply.Content)
}

// TestIntegration_CreateHandler_ForceSearch_LiveWebSearch is ADR-008
// batch B's mandatory live proof, part two: the same real end-to-end
// path as above, except the room's own web_search_enabled toggle is OFF
// — search only happens because this one message forces it via
// force_search. Proves the two trigger paths are genuinely independent
// against the real API, not just against a fake in the tests above.
//
// Skips without OPENAI_API_KEY; incurs a small real cost when it runs.
func TestIntegration_CreateHandler_ForceSearch_LiveWebSearch(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set; skipping live web search integration test")
	}
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-live-force-search-")
	// web_search_enabled is left at its default (off) — this room's own
	// toggle plays no part; only force_search on the request below does.
	rm, err := rooms.Create(ctx, &owner.ID, "live-force-search-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Pong", agent.ProviderOpenAI, nil, "hash-live-force-search")
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	rec := newRecordingHub(rm.ID)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, rec, rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	body := `{"content":"@Pong what is one real current technology news headline from today?","mentioned_agent_ids":["` +
		a.ID.String() + `"],"force_search":true}`
	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}

	var mention Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	reply := waitForRealReplyMessage(t, ctx, s, rm.ID, mention.ID)

	if reply.Content == "" {
		t.Fatal("expected a real, non-empty reply")
	}
	if !strings.Contains(reply.Content, "**Sources**") || !strings.Contains(reply.Content, "http") {
		t.Fatalf("reply.Content = %q, want a real citation rendered via ApplyCitations (a Sources list with a real URL), proving forced search worked in a room whose own toggle is off", reply.Content)
	}
	t.Logf("live forced-search reply (room toggle OFF): %q", reply.Content)
}
