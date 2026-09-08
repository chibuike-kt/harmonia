package message

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/provider/anthropic"
	"github.com/chibuike-kt/harmonia/internal/provider/openai"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// generationTimeout bounds one @mention's whole invocation — agent
// lookup, credential resolution, and the live provider call together.
// Long enough for a real generation, short enough that a hung provider
// call doesn't leave a goroutine (and an agent stuck showing "running")
// alive indefinitely.
const generationTimeout = 2 * time.Minute

// cascadeDepthCap bounds how many agent-to-agent hops (ADR-006 batch B)
// can chain off a single human-triggered mention, enforced regardless of
// a room's agent_cascading_enabled setting — that flag decides whether a
// chain can start at all, this decides how far it can go once it does.
// Two agents that always mention each other back is a real infinite-loop
// shape; every hop is also a real charge against someone's own BYOK key.
const cascadeDepthCap = 3

// mentionAgentToolName is the one tool offered to a generation when its
// room has cascading enabled — the structured way an agent's own reply
// expresses "bring Agent X into this," never text-scanned out of the
// reply the way ADR-004 already rejected for human mentions. Named
// deliberately close to batch C's own create_task/request_handoff: one
// tool-use mechanism, not a bespoke parallel one just for mentions.
const mentionAgentToolName = "mention_agent"

// mentionAgentTool is the tool definition offered to a generation when
// its room has cascading enabled. Its schema takes the target's display
// name, not an internal ID: a model only ever sees agents in the
// conversation by name, the same way a human addressing another agent
// does — resolveMentionToolCalls does the actual name-to-ID lookup,
// scoped to this room, never trusting anything the model supplies as an
// ID directly.
func mentionAgentTool() provider.ToolDef {
	return provider.ToolDef{
		Name: mentionAgentToolName,
		Description: "Bring another agent already in this room into the " +
			"conversation, the same way a human @mentions someone. Only " +
			"call this when that agent's specific expertise or action is " +
			"genuinely needed to continue — not on every turn, and not " +
			"more than once for the same agent.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_name": map[string]any{
					"type":        "string",
					"description": "The exact display name of the agent to mention, as it appears in this room's conversation.",
				},
			},
			"required": []string{"agent_name"},
		},
	}
}

// resolveMentionToolCalls turns a generation's mention_agent tool calls
// into real agent IDs — matched by exact case-insensitive name against
// every agent actually in this room, never a raw ID the model might
// invent, the same non-leaking validation every mention path in this
// package already uses. A call naming an agent that doesn't exist here,
// or repeating one already resolved, is dropped rather than treated as a
// failure: the reply itself already generated successfully, and a model
// naming the wrong agent shouldn't cost the human a lost reply over it.
func resolveMentionToolCalls(calls []provider.ToolCall, roomAgents []agent.Agent) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(calls))
	var ids []uuid.UUID
	for _, call := range calls {
		if call.Name != mentionAgentToolName {
			continue
		}
		name, _ := call.Input["agent_name"].(string)
		id, ok := resolveAgentName(name, roomAgents)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// newProviderClientFunc builds the env-var-fallback provider client — a
// field on Orchestrator, not a bare call to the package-level
// newProviderClient, so a test can substitute a fake and exercise the
// whole invocation flow (status transitions, history assembly, reply
// storage, panic recovery) without a real network call or a stored BYOK
// credential — the same seam credentials.Store itself uses for the same
// reason.
type newProviderClientFunc func(providerName agent.Provider, apiKey string) (provider.Agent, error)

// Orchestrator generates an agent's reply to an @mention asynchronously,
// off the request goroutine that received it (ADR-004: invocation must
// never block the human's POST on a live provider call). One per server
// process, holding the same shared dependencies every other handler
// already uses — not a job queue, since this codebase doesn't have one
// yet and this doesn't need one (see the build brief).
type Orchestrator struct {
	messages          *Store
	agents            *agent.Store
	credentials       *credentials.Store
	users             *user.Store
	rooms             *room.Store
	tasks             *task.Store
	hub               realtime.Publisher
	rdb               *redis.Client
	// beginner starts the transactions create_task/request_handoff need
	// (ADR-006 batch C) — everything before batch C only ever needed
	// plain reads/writes through the Stores above, no transaction of its
	// own to open.
	beginner          store.Beginner
	newProviderClient newProviderClientFunc
}

func NewOrchestrator(messages *Store, agents *agent.Store, creds *credentials.Store, users *user.Store, rooms *room.Store, tasks *task.Store, beginner store.Beginner, hub realtime.Publisher, rdb *redis.Client) *Orchestrator {
	return &Orchestrator{
		messages: messages, agents: agents, credentials: creds, users: users, rooms: rooms, tasks: tasks, beginner: beginner, hub: hub, rdb: rdb,
		newProviderClient: newProviderClient,
	}
}

// TriggerReply launches, in a new goroutine, generation of
// mentionedAgentID's reply to triggering — the human (or, at depth > 0,
// agent — ADR-006 batch B) message that mentioned it. Returns
// immediately; the reply (or a visible failure message, per ADR-004) is
// delivered later over hub. roomOwnerID is the room's owner as already
// resolved by the caller's own ownership check, passed through rather
// than re-fetched here. depth is how many agent-to-agent hops already
// led to this invocation — 0 for every human-triggered mention,
// incremented by one each time an agent's own reply cascades into
// another; see invoke and cascadeDepthCap for where it's enforced.
//
// This goroutine can outlive the request that spawned it — the deferred
// recover is not decorative. A panic here must never crash the process,
// and must never leave the agent stuck showing "running" forever with no
// explanation: both are handled the same way a live provider error is,
// by falling through to a visible failure message and resetting status.
// The context is deliberately independent of the request's (which is
// canceled the moment the response is written), bounded by its own
// generationTimeout instead.
func (o *Orchestrator) TriggerReply(mentionedAgentID uuid.UUID, roomOwnerID *uuid.UUID, triggering Message, depth int) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), generationTimeout)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ERROR message: panic generating reply for agent %s: %v", mentionedAgentID, r)
				o.fail(ctx, triggering.RoomID, mentionedAgentID, triggering.ID,
					"something went wrong generating this reply and it couldn't complete.")
			}
		}()
		o.invoke(ctx, mentionedAgentID, roomOwnerID, triggering, depth)
	}()
}

func (o *Orchestrator) invoke(ctx context.Context, agentID uuid.UUID, roomOwnerID *uuid.UUID, triggering Message, depth int) {
	a, err := o.agents.GetByID(ctx, agentID)
	if err != nil {
		log.Printf("ERROR message: load agent %s to generate reply: %v", agentID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, "the mentioned agent couldn't be loaded.")
		return
	}
	// Read before this invocation's own setStatus below overwrites it —
	// ADR-007 batch A's busy-redirect signal is exactly "was this agent
	// already running when THIS invocation started," which only this
	// pre-overwrite read can answer. Whether it actually changes anything
	// is decided later, once cascadingEnabled is known (redirecting only
	// makes sense when the room's opted into agent-to-agent coordination
	// at all — same gate ADR-006 batch B already uses, no new toggle).
	wasBusy := a.Status == agent.StatusRunning

	o.setStatus(ctx, triggering.RoomID, agentID, agent.StatusRunning)

	client, err := o.resolveClient(ctx, roomOwnerID, a)
	if err != nil {
		log.Printf("ERROR message: resolve provider client for agent %s: %v", agentID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, resolveFailureReason(err))
		return
	}

	history, err := o.messages.ListByRoom(ctx, triggering.RoomID)
	if err != nil {
		log.Printf("ERROR message: load history for room %s: %v", triggering.RoomID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, "recent conversation history couldn't be loaded.")
		return
	}

	// Cascading is opt-in per room (ADR-006 batch B) — the mention_agent
	// tool is only ever offered when this room has turned it on. A lookup
	// failure here is treated as "cascading off" rather than failing the
	// whole reply: this is a secondary capability check, not something a
	// human's mention should ever be lost over.
	cascadingEnabled := false
	rm, err := o.rooms.GetByID(ctx, triggering.RoomID)
	if err != nil {
		log.Printf("ERROR message: load room %s to check cascading: %v", triggering.RoomID, err)
	} else {
		cascadingEnabled = rm.AgentCascadingEnabled
	}

	// Loaded once, up front — needed both to build request_handoff's own
	// description (the model can only ever hand off to an agent it's
	// actually been told the name of, the same reasoning mention_agent's
	// own resolution already applies) and, after the call, to resolve
	// whatever name the model actually supplied. A lookup failure here
	// degrades to "no other agents visible this turn" rather than
	// failing the whole reply over a secondary capability.
	roomAgents, err := o.agents.ListByRoom(ctx, triggering.RoomID)
	if err != nil {
		log.Printf("ERROR message: load room %s agents: %v", triggering.RoomID, err)
	}
	otherAgents := make([]agent.Agent, 0, len(roomAgents))
	for _, ra := range roomAgents {
		if ra.ID != agentID {
			otherAgents = append(otherAgents, ra)
		}
	}

	// create_task is always offered (ADR-006 batch C: low-stakes, already
	// an ordinary operation) — request_handoff only when there's both a
	// real open task and a real other agent to reference, since its
	// task_id/to_agent_name are validated against exactly those sets.
	activeTasks, err := o.tasks.ListActiveByRoom(ctx, triggering.RoomID)
	if err != nil {
		log.Printf("ERROR message: load room %s active tasks: %v", triggering.RoomID, err)
	}

	customInstructions, ownerName := o.loadOwnerContext(ctx, roomOwnerID)
	framing := roomFraming{
		roomName:        rm.Name,
		objective:       objectiveFrom(history),
		ownerName:       ownerName,
		otherAgentNames: agentNames(otherAgents),
		// ADR-007 batch A: only worth telling the model it's busy and may
		// redirect when there's a real mechanism for redirecting at all —
		// mention_agent isn't even offered below unless cascading is on.
		busy: wasBusy && cascadingEnabled,
	}
	req := buildGenerateRequest(a, history, customInstructions, framing)
	req.Tools = append(req.Tools, createTaskTool())
	if cascadingEnabled {
		req.Tools = append(req.Tools, mentionAgentTool())
	}
	if len(activeTasks) > 0 && len(otherAgents) > 0 {
		req.Tools = append(req.Tools, requestHandoffTool(activeTasks, otherAgents))
	}

	resp, err := client.Generate(ctx, req)
	if err != nil {
		log.Printf("ERROR message: generate reply for agent %s: %v", agentID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, fmt.Sprintf("the provider call failed: %v", err))
		return
	}

	var cascadeTargets []uuid.UUID
	var createTaskCalls, requestHandoffCalls []provider.ToolCall
	if cascadingEnabled {
		cascadeTargets = resolveMentionToolCalls(resp.ToolCalls, roomAgents)
	}
	for _, call := range resp.ToolCalls {
		switch call.Name {
		case createTaskToolName:
			createTaskCalls = append(createTaskCalls, call)
		case requestHandoffToolName:
			requestHandoffCalls = append(requestHandoffCalls, call)
		}
	}

	reply, err := o.messages.CreateAgent(ctx, triggering.RoomID, agentID, resp.Content, triggering.ID, &resp.InputTokens, &resp.OutputTokens, cascadeTargets)
	if err != nil {
		log.Printf("ERROR message: store generated reply for agent %s: %v", agentID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, "the reply was generated but couldn't be saved.")
		return
	}
	o.hub.Publish(triggering.RoomID, realtime.NewChatMessage(toChatMessage(reply)))
	o.setStatus(ctx, triggering.RoomID, agentID, agent.StatusAvailable)

	// create_task executes immediately (ADR-006 batch C) — the same
	// effect as a human hitting POST /v1/tasks directly, including the
	// same TASK_CREATED audit event, so it shows up in the timeline
	// exactly like any other task, chat-triggered or not.
	for _, call := range createTaskCalls {
		o.executeCreateTask(ctx, triggering.RoomID, agentID, call.Input)
	}
	// request_handoff only ever creates a pending proposal here — ADR-006
	// is explicit that this does not execute a real handoff. See
	// executeApprovedHandoff for the one place that actually does, gated
	// on a human's approval.
	for _, call := range requestHandoffCalls {
		o.executeRequestHandoff(ctx, triggering.RoomID, agentID, call.Input)
	}

	// Cascade into each mentioned agent — one invocation per mention, same
	// as a human's own multi-mention (ADR-006 batch A) — unless the hop
	// cap is hit, in which case that agent is never actually invoked: a
	// plain, visible message says so instead of a silent stop (ADR-006
	// batch B). cascadingEnabled is re-checked here rather than assumed
	// from cascadeTargets being non-empty: cascadeTargets can only be
	// non-empty when it was already true, but the guard is cheap and
	// keeps this block correct even if that invariant ever changes.
	for _, targetID := range cascadeTargets {
		if !cascadingEnabled {
			break
		}
		if depth+1 > cascadeDepthCap {
			o.cascadeCapped(ctx, triggering.RoomID, targetID, reply.ID, cascadeDepthCap)
			continue
		}
		o.TriggerReply(targetID, roomOwnerID, reply, depth+1)
	}
}

// fail inserts and publishes a visible failure message — ADR-004: a
// failed generation is a real message explaining what happened, never a
// silent drop — then resets the agent's status. Framed as coming from
// the agent itself (sender_kind 'agent', same as a real reply) so it
// renders in the same place a reply would have, rather than as some
// separate system-message concept this phase doesn't build.
func (o *Orchestrator) fail(ctx context.Context, roomID, agentID uuid.UUID, replyToMessageID uuid.UUID, reason string) {
	content := "I couldn't generate a reply — " + reason
	msg, err := o.messages.CreateAgent(ctx, roomID, agentID, content, replyToMessageID, nil, nil, nil)
	if err != nil {
		log.Printf("ERROR message: store failure message for agent %s: %v", agentID, err)
	} else {
		o.hub.Publish(roomID, realtime.NewChatMessage(toChatMessage(msg)))
	}
	o.setStatus(ctx, roomID, agentID, agent.StatusAvailable)
}

// cascadeCapped posts the visible, honest stop message ADR-006 batch B
// requires when a cascade hits its hop limit — mentionedAgentID is never
// actually invoked here (no status transition, no generation, no cost):
// this is purely a record that the chain stopped and why, framed as
// coming from the agent that would have been invoked next so it reads
// naturally in the timeline, the same convention fail already
// establishes for a failed invocation rather than inventing a separate
// system-message concept.
func (o *Orchestrator) cascadeCapped(ctx context.Context, roomID, mentionedAgentID, replyToMessageID uuid.UUID, cap int) {
	content := fmt.Sprintf("this room's automatic agent-to-agent chain reached its %d-hop limit here and stopped — a human can continue the conversation.", cap)
	msg, err := o.messages.CreateAgent(ctx, roomID, mentionedAgentID, content, replyToMessageID, nil, nil, nil)
	if err != nil {
		log.Printf("ERROR message: store cascade-stopped message for agent %s: %v", mentionedAgentID, err)
		return
	}
	o.hub.Publish(roomID, realtime.NewChatMessage(toChatMessage(msg)))
}

// setStatus persists agentID's new status and publishes the transition —
// the same running/available typing signal task claim/complete already
// drive, reused here rather than a new status value (ADR-004). Unlike
// task/http.go's own publishPresence, this also performs the SetStatus
// write itself: there's no shared transaction here for a caller to have
// already written it into, since this whole invocation runs well after
// the request (and its transaction) that triggered it is long done.
func (o *Orchestrator) setStatus(ctx context.Context, roomID, agentID uuid.UUID, status agent.Status) {
	if err := o.agents.SetStatus(ctx, agentID, status); err != nil {
		log.Printf("ERROR message: set agent %s status to %s: %v", agentID, status, err)
		return
	}
	o.hub.Publish(roomID, realtime.NewPresenceMessage(agentID, string(status)))
	if err := realtime.SetPresence(ctx, o.rdb, agentID, string(status)); err != nil {
		log.Printf("ERROR message: mirror presence for agent %s: %v", agentID, err)
	}
}

// resolveClient resolves a.Provider's client for this invocation:
// credentials.Store.Resolve first (the room owner's BYOK credential —
// this is the production path, and the whole reason Resolve was built),
// falling back to the provider's env var only when Resolve reports no
// credential is configured at all — the same dev-path convenience the
// provider packages' own integration tests already use
// (ANTHROPIC_API_KEY / OPENAI_API_KEY), never the platform's real
// credential path (see credentials.Store.Resolve's own doc comment).
func (o *Orchestrator) resolveClient(ctx context.Context, roomOwnerID *uuid.UUID, a agent.Agent) (provider.Agent, error) {
	client, err := o.credentials.Resolve(ctx, roomOwnerID, a.Provider)
	if err == nil {
		return client, nil
	}
	if !errors.Is(err, credentials.ErrNoCredential) && !errors.Is(err, credentials.ErrEncryptionNotConfigured) {
		return nil, err
	}

	envKey := envKeyFor(a.Provider)
	if envKey == "" {
		return nil, fmt.Errorf("message: unsupported provider %q", a.Provider)
	}
	apiKey := os.Getenv(envKey)
	if apiKey == "" {
		return nil, fmt.Errorf("no credential connected for %s and %s is not set", a.Provider, envKey)
	}
	return o.newProviderClient(a.Provider, apiKey)
}

// loadOwnerContext returns roomOwnerID's custom instructions (ADR-005)
// and casual display name (ADR-004's 2026-09-07 room-framing addendum)
// in one lookup — "" for either if there's no owner, the field isn't
// set, or the lookup fails. Both are soft context, not a hard
// requirement: a human's message is waiting on a real reply either way,
// and neither is worth failing the whole generation over if this one
// lookup has a transient problem.
func (o *Orchestrator) loadOwnerContext(ctx context.Context, roomOwnerID *uuid.UUID) (customInstructions, ownerName string) {
	if roomOwnerID == nil {
		return "", ""
	}
	owner, err := o.users.GetByID(ctx, *roomOwnerID)
	if err != nil {
		log.Printf("ERROR message: load owner %s for room context: %v", *roomOwnerID, err)
		return "", ""
	}
	if owner.CustomInstructions != nil {
		customInstructions = *owner.CustomInstructions
	}
	return customInstructions, ownerDisplayName(owner)
}

// ownerDisplayName picks what an agent should call the room's human
// owner — PreferredName first (ADR-005: "what agents/the app call
// someone casually"), falling back through DisplayName to the OAuth
// Username, which always exists.
func ownerDisplayName(u user.User) string {
	if u.PreferredName != nil && *u.PreferredName != "" {
		return *u.PreferredName
	}
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.Username
}

// agentNames extracts display names, in order — shared by roomFraming
// assembly and (already, elsewhere in this package) tool descriptions
// that list a room's other agents by name.
func agentNames(agents []agent.Agent) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}

// framingObjectiveCap bounds how much of the stand-in "objective"
// message reaches the system prompt — a large pasted block as a room's
// first message shouldn't balloon every subsequent invocation's framing.
const framingObjectiveCap = 300

// objectiveFrom mirrors the frontend's own RoomInfoPanel convention
// exactly (see its own comment): there is no real captured "objective"
// field on a room, so the oldest message in the currently-loaded history
// stands in for one. That's the room's true first message only while
// the room has fewer messages than recencyLimit — past that, this is
// honestly just "the oldest message still in view," not a guarantee of
// the room's actual original objective. Good enough for framing context,
// not offered anywhere as a claim of precision.
func objectiveFrom(history []Message) string {
	if len(history) == 0 {
		return ""
	}
	content := history[0].Content
	if len(content) > framingObjectiveCap {
		return content[:framingObjectiveCap] + "…"
	}
	return content
}

// roomFraming carries the "what room is this, and who else is in it"
// context ADR-004's 2026-09-07 addendum requires: without it, a model
// invoked twice in the same room has no idea it's one of several
// participants in anything — the addendum names this directly as why
// two invocations of the same agent could produce unrelated,
// uncoordinated answers even setting the duplicate-mention bug aside.
// Assembled fresh per invocation, not cached: a room's name and agent
// roster can both change between calls.
type roomFraming struct {
	roomName        string
	objective       string
	ownerName       string
	otherAgentNames []string
	// busy is ADR-007 batch A's own addition: true only when this
	// invocation's agent was already running another generation when
	// this one started, AND the room has cascading enabled (the only
	// case where mention_agent — the redirect mechanism — is actually
	// offered as a tool below). Never true otherwise, so describe()
	// never dangles a redirect instruction with nothing to back it.
	busy bool
}

// describe renders the framing as a short paragraph appended to the
// system prompt — plain prose, not a structured block, so it reads
// naturally alongside the generic role sentence it follows.
func (f roomFraming) describe() string {
	var b strings.Builder
	name := f.roomName
	if name == "" {
		name = "this room"
	}
	fmt.Fprintf(&b, "You're in a room called %q.", name)
	if f.objective != "" {
		fmt.Fprintf(&b, " It started with: %q.", f.objective)
	}
	if len(f.otherAgentNames) > 0 {
		fmt.Fprintf(&b, " Other agents also in this room: %s.", strings.Join(f.otherAgentNames, ", "))
	} else {
		b.WriteString(" You're currently the only agent in this room.")
	}
	if f.ownerName != "" {
		fmt.Fprintf(&b, " Its human owner is %s.", f.ownerName)
	}
	// ADR-007 batch A: reuses ADR-006 batch B's mention_agent mechanism
	// entirely — no new tool, no new plumbing, just telling the model
	// the one fact it needs to decide whether redirecting is the right
	// call. The visible "redirect" message this produces is simply this
	// agent's own real reply, cascading into the target exactly the way
	// any other mention_agent call already does.
	if f.busy {
		b.WriteString(" You're currently already generating a reply to something else in this room. If another agent here is free and better placed to answer this new message, you may hand it off with mention_agent instead of answering it yourself — say so plainly in your reply (for example: \"I'm already working on something else, so I'll let @Name take this one.\"). If no one else is free or suitable, just answer normally.")
	}
	return b.String()
}

// resolveFailureReason turns a resolveClient error into the visible
// failure message's explanation — specific enough for a human to act on
// (connect a credential, or set the dev env var) without leaking
// anything about the failure's internals.
func resolveFailureReason(err error) string {
	if errors.Is(err, credentials.ErrNoCredential) {
		return "no provider credential is connected for this room's owner, and no fallback key is configured."
	}
	if errors.Is(err, credentials.ErrEncryptionNotConfigured) {
		return "credential storage isn't configured on this server, and no fallback key is configured."
	}
	return "a provider credential for this agent couldn't be resolved."
}

func envKeyFor(p agent.Provider) string {
	switch p {
	case agent.ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case agent.ProviderOpenAI:
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}

// newProviderClient mirrors credentials package's own unexported
// defaultProviderClient — duplicated rather than imported since that one
// isn't exported, for the same env-var dev-path fallback every provider
// package's own integration tests already use.
func newProviderClient(providerName agent.Provider, apiKey string) (provider.Agent, error) {
	switch providerName {
	case agent.ProviderAnthropic:
		return anthropic.New(apiKey), nil
	case agent.ProviderOpenAI:
		return openai.New(apiKey), nil
	default:
		return nil, fmt.Errorf("message: unsupported provider %q", providerName)
	}
}

// buildGenerateRequest formats the recency window as a conversation for
// Generate — v1's entire context-assembly story (ADR-004): the last N
// messages, not a relevance-ranked engine. A message authored by the
// agent being invoked is the "assistant" turn; everything else (the
// human, or — once multi-agent rooms actually get turn-taking depth,
// see ADR-004's Revisit When — another agent) is a "user" turn, since
// provider.Message only has the two roles Chat Completions/Messages
// APIs support. This phase proves the loop with one agent without
// hard-coding it, but doesn't build multi-agent conversational depth
// (name-attributing a third party's turns) — that's real, later work.
func buildGenerateRequest(a agent.Agent, history []Message, customInstructions string, framing roomFraming) provider.GenerateRequest {
	systemPrompt := fmt.Sprintf(
		"You are %s, an AI agent participating in a chat room in Harmonia, a tool for coordinating work between humans and AI agents. Respond naturally and helpfully to the conversation below.\n\n%s",
		a.Name,
		framing.describe(),
	)
	// Prepended, not appended: the room owner's own standing preference
	// for how any agent should respond takes precedence over this
	// generic role framing, the same "prepend it" placement the build
	// brief specifies (ADR-005) — same spot the recency-window history
	// itself gets assembled relative to the rest of the prompt.
	if customInstructions != "" {
		systemPrompt = fmt.Sprintf("%s\n\n%s", customInstructions, systemPrompt)
	}
	msgs := make([]provider.Message, 0, len(history))
	for _, m := range history {
		role := "user"
		if m.SenderKind == SenderAgent && m.AgentID != nil && *m.AgentID == a.ID {
			role = "assistant"
		}
		msgs = append(msgs, provider.Message{Role: role, Content: m.Content})
	}
	return provider.GenerateRequest{SystemPrompt: systemPrompt, Messages: msgs}
}

func toChatMessage(m Message) realtime.ChatMessage {
	return realtime.ChatMessage{
		ID:                m.ID,
		RoomID:            m.RoomID,
		SenderKind:        string(m.SenderKind),
		UserID:            m.UserID,
		AgentID:           m.AgentID,
		MentionedAgentIDs: m.MentionedAgentIDs,
		ReplyToMessageID:  m.ReplyToMessageID,
		Content:           m.Content,
		CreatedAt:         m.CreatedAt,
		InputTokens:       m.InputTokens,
		OutputTokens:      m.OutputTokens,
	}
}
