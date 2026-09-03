// Package integration holds the Milestone 1 acceptance test: the single
// test that proves the milestone, driven against the real HTTP API with
// real Postgres and real provider calls, not fixtures.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/httpapi"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/provider/anthropic"
	"github.com/chibuike-kt/harmonia/internal/provider/openai"
	"github.com/chibuike-kt/harmonia/internal/store"
)

// fromAgentProvider and toAgentProvider are the only two lines that pick
// which real provider generates each agent's content. Both are "openai"
// today because there is no ANTHROPIC_API_KEY available yet — change
// either to "anthropic" to run the real cross-provider version once a key
// exists. Nothing else in this test needs to change.
const (
	fromAgentProvider = "openai"
	toAgentProvider   = "openai"
)

// newProviderClient builds the real client for kind, or skips the test if
// the credential it needs isn't set.
func newProviderClient(t *testing.T, kind string) provider.Agent {
	t.Helper()
	switch kind {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			t.Skip("ANTHROPIC_API_KEY not set; skipping acceptance test")
		}
		return anthropic.New(key)
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			t.Skip("OPENAI_API_KEY not set; skipping acceptance test")
		}
		return openai.New(key)
	default:
		t.Fatalf("unknown provider kind %q", kind)
		return nil
	}
}

// TestIntegration_MilestoneOneAcceptance walks the Milestone 1 acceptance
// sequence against the real HTTP API — a live httptest.Server wrapping
// the actual production router (internal/httpapi.NewRouter), backed by
// real Postgres: create a room, register two agents, create a task, claim
// it, complete it, query its context, request a handoff, accept the
// handoff, then assert the room's event log is complete and ordered.
//
// The task objective and the handoff summary are real generated content
// from the configured providers (see newProviderClient), not fixtures.
//
// This run uses OpenAI for both agents — see the test report for why that
// does not, on its own, satisfy the milestone's actual definition of
// done, which calls for two distinct real providers.
func TestIntegration_MilestoneOneAcceptance(t *testing.T) {
	dbURL := os.Getenv("HARMONIA_DATABASE_URL")
	if dbURL == "" {
		t.Skip("HARMONIA_DATABASE_URL not set; skipping acceptance test")
	}

	fromClient := newProviderClient(t, fromAgentProvider)
	toClient := newProviderClient(t, toAgentProvider)

	ctx := context.Background()
	st, err := store.New(ctx, dbURL, os.Getenv("HARMONIA_REDIS_ADDR"))
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	defer st.Close()

	srv := httptest.NewServer(httpapi.NewRouter(st))
	defer srv.Close()

	h := &harness{t: t, baseURL: srv.URL, client: srv.Client()}

	// Step 1: room creation.
	roomID := h.createRoom("milestone-1-acceptance-" + uuid.New().String())

	// Step 2: two agent registrations.
	fromAgentID, fromKey := h.registerAgent(roomID, "researcher", fromAgentProvider)
	toAgentID, toKey := h.registerAgent(roomID, "writer", toAgentProvider)

	// Real content: the task objective comes from the "from" agent's
	// provider, not a fixture.
	objective := h.generate(ctx, fromClient,
		"You write concise, one-sentence task objectives. Reply with only the sentence.",
		"Give a one-sentence objective for researching Go error handling idioms.")

	// Step 3: TASK.CREATE
	tk := h.createTask(fromKey, objective)
	if tk.Objective != objective {
		t.Fatalf("created task Objective = %q, want %q", tk.Objective, objective)
	}

	// Step 4: TASK.CLAIM — by the "to" agent, who will complete the work
	// and hand it back.
	claimed := h.claimTask(toKey, tk.ID)
	if claimed.OwnerAgentID == nil || *claimed.OwnerAgentID != toAgentID {
		t.Fatalf("claimed task OwnerAgentID = %v, want %s", claimed.OwnerAgentID, toAgentID)
	}

	// Step 5: TASK.COMPLETE
	completed := h.completeTask(toKey, tk.ID)
	if completed.Status != "COMPLETED" {
		t.Fatalf("completed task Status = %q, want %q", completed.Status, "COMPLETED")
	}

	// Step 6: CONTEXT.REQUEST — the "from" agent checks in on the task it
	// created.
	ctxResult := h.requestContext(fromKey, tk.ID)
	if ctxResult.Status != "COMPLETED" {
		t.Fatalf("context Status = %q, want %q", ctxResult.Status, "COMPLETED")
	}

	// Real content: the handoff summary comes from the "to" agent's
	// provider, not a fixture.
	summary := h.generate(ctx, toClient,
		"You write concise, one-sentence handoff summaries. Reply with only the sentence.",
		fmt.Sprintf("Summarize completing the task %q in one sentence.", objective))

	// Step 7: HANDOFF.REQUEST — the "to" agent hands the completed work
	// back to the "from" agent.
	ho := h.requestHandoff(toKey, tk.ID, fromAgentID, summary)
	if ho.ToAgentID != fromAgentID {
		t.Fatalf("handoff ToAgentID = %s, want %s", ho.ToAgentID, fromAgentID)
	}

	// Step 8: HANDOFF.ACCEPT
	accepted := h.acceptHandoff(fromKey, ho.ID)
	if accepted.Status != "ACCEPTED" {
		t.Fatalf("accepted handoff Status = %q, want %q", accepted.Status, "ACCEPTED")
	}

	// Assertion: the room's event log is complete and ordered.
	gotTypes := h.listEventTypes(fromKey, roomID)
	wantTypes := []string{
		"TASK_CREATED",
		"TASK_CLAIMED",
		"TASK_COMPLETED",
		"CONTEXT_REQUESTED",
		"HANDOFF_REQUESTED",
		"HANDOFF_ACCEPTED",
	}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
	}
	for i, want := range wantTypes {
		if gotTypes[i] != want {
			t.Fatalf("event[%d] = %q, want %q", i, gotTypes[i], want)
		}
	}
}

// harness drives the real HTTP API over an actual net/http.Client against
// an httptest.Server — no handler functions called directly.
type harness struct {
	t       *testing.T
	baseURL string
	client  *http.Client
}

// request makes one HTTP call and, in this same function so the body's
// lifetime is never split across call sites, reads, closes, checks the
// status, and decodes it into out (skipped if out is nil).
func (h *harness) request(method, path, bearer string, body any, wantStatus int, out any) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.baseURL+path, reader)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("%s %s: read response: %v", method, path, err)
	}
	if resp.StatusCode != wantStatus {
		h.t.Fatalf("%s %s: status = %d, want %d, body = %s", method, path, resp.StatusCode, wantStatus, respBody)
	}
	if out == nil {
		return
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		h.t.Fatalf("%s %s: decode response: %v", method, path, err)
	}
}

func (h *harness) createRoom(name string) uuid.UUID {
	h.t.Helper()
	var got struct {
		ID uuid.UUID `json:"id"`
	}
	h.request(http.MethodPost, "/v1/rooms", "", map[string]any{"name": name}, http.StatusCreated, &got)
	return got.ID
}

func (h *harness) registerAgent(roomID uuid.UUID, name, providerKind string) (uuid.UUID, string) {
	h.t.Helper()
	var got struct {
		ID     uuid.UUID `json:"id"`
		APIKey string    `json:"api_key"`
	}
	h.request(http.MethodPost, "/v1/rooms/"+roomID.String()+"/agents", "", map[string]any{
		"name":     name,
		"provider": providerKind,
	}, http.StatusCreated, &got)
	return got.ID, got.APIKey
}

type taskResult struct {
	ID           uuid.UUID  `json:"id"`
	OwnerAgentID *uuid.UUID `json:"owner_agent_id,omitempty"`
	Objective    string     `json:"objective"`
	Status       string     `json:"status"`
}

func (h *harness) createTask(bearer, objective string) taskResult {
	h.t.Helper()
	var got taskResult
	h.request(http.MethodPost, "/v1/tasks", bearer, map[string]any{"objective": objective}, http.StatusCreated, &got)
	return got
}

func (h *harness) claimTask(bearer string, taskID uuid.UUID) taskResult {
	h.t.Helper()
	var got taskResult
	h.request(http.MethodPost, "/v1/tasks/"+taskID.String()+"/claim", bearer, nil, http.StatusOK, &got)
	return got
}

func (h *harness) completeTask(bearer string, taskID uuid.UUID) taskResult {
	h.t.Helper()
	var got taskResult
	h.request(http.MethodPost, "/v1/tasks/"+taskID.String()+"/complete", bearer, nil, http.StatusOK, &got)
	return got
}

type contextResult struct {
	TaskID    uuid.UUID `json:"task_id"`
	Objective string    `json:"objective"`
	Status    string    `json:"status"`
}

func (h *harness) requestContext(bearer string, taskID uuid.UUID) contextResult {
	h.t.Helper()
	var got contextResult
	h.request(http.MethodGet, "/v1/context/tasks/"+taskID.String(), bearer, nil, http.StatusOK, &got)
	return got
}

type handoffResult struct {
	ID        uuid.UUID `json:"id"`
	ToAgentID uuid.UUID `json:"to_agent_id"`
	Status    string    `json:"status"`
}

func (h *harness) requestHandoff(bearer string, taskID, toAgentID uuid.UUID, summary string) handoffResult {
	h.t.Helper()
	var got handoffResult
	h.request(http.MethodPost, "/v1/handoffs", bearer, map[string]any{
		"task_id":     taskID,
		"to_agent_id": toAgentID,
		"summary":     summary,
	}, http.StatusCreated, &got)
	return got
}

func (h *harness) acceptHandoff(bearer string, handoffID uuid.UUID) handoffResult {
	h.t.Helper()
	var got handoffResult
	h.request(http.MethodPost, "/v1/handoffs/"+handoffID.String()+"/accept", bearer, nil, http.StatusOK, &got)
	return got
}

func (h *harness) listEventTypes(bearer string, roomID uuid.UUID) []string {
	h.t.Helper()
	var got []struct {
		Type string `json:"type"`
	}
	h.request(http.MethodGet, "/v1/rooms/"+roomID.String()+"/events", bearer, nil, http.StatusOK, &got)
	types := make([]string, len(got))
	for i, e := range got {
		types[i] = e.Type
	}
	return types
}

func (h *harness) generate(ctx context.Context, client provider.Agent, systemPrompt, userMessage string) string {
	h.t.Helper()
	resp, err := client.Generate(ctx, provider.GenerateRequest{
		SystemPrompt: systemPrompt,
		Messages:     []provider.Message{{Role: "user", Content: userMessage}},
	})
	if err != nil {
		h.t.Fatalf("generate content: %v", err)
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		h.t.Fatal("generate content: empty response")
	}
	return content
}
