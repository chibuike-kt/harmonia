package message

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/provider/anthropic"
	"github.com/chibuike-kt/harmonia/internal/provider/openai"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// generationTimeout bounds one @mention's whole invocation — agent
// lookup, credential resolution, and the live provider call together.
// Long enough for a real generation, short enough that a hung provider
// call doesn't leave a goroutine (and an agent stuck showing "running")
// alive indefinitely.
const generationTimeout = 2 * time.Minute

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
	hub               realtime.Publisher
	rdb               *redis.Client
	newProviderClient newProviderClientFunc
}

func NewOrchestrator(messages *Store, agents *agent.Store, creds *credentials.Store, users *user.Store, hub realtime.Publisher, rdb *redis.Client) *Orchestrator {
	return &Orchestrator{
		messages: messages, agents: agents, credentials: creds, users: users, hub: hub, rdb: rdb,
		newProviderClient: newProviderClient,
	}
}

// TriggerReply launches, in a new goroutine, generation of
// mentionedAgentID's reply to triggering — the human message that
// @mentioned it. Returns immediately; the reply (or a visible failure
// message, per ADR-004) is delivered later over hub. roomOwnerID is the
// room's owner as already resolved by the HTTP handler's own ownership
// check, passed through rather than re-fetched here.
//
// This goroutine can outlive the request that spawned it — the deferred
// recover is not decorative. A panic here must never crash the process,
// and must never leave the agent stuck showing "running" forever with no
// explanation: both are handled the same way a live provider error is,
// by falling through to a visible failure message and resetting status.
// The context is deliberately independent of the request's (which is
// canceled the moment the response is written), bounded by its own
// generationTimeout instead.
func (o *Orchestrator) TriggerReply(mentionedAgentID uuid.UUID, roomOwnerID *uuid.UUID, triggering Message) {
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
		o.invoke(ctx, mentionedAgentID, roomOwnerID, triggering)
	}()
}

func (o *Orchestrator) invoke(ctx context.Context, agentID uuid.UUID, roomOwnerID *uuid.UUID, triggering Message) {
	o.setStatus(ctx, triggering.RoomID, agentID, agent.StatusRunning)

	a, err := o.agents.GetByID(ctx, agentID)
	if err != nil {
		log.Printf("ERROR message: load agent %s to generate reply: %v", agentID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, "the mentioned agent couldn't be loaded.")
		return
	}

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

	resp, err := client.Generate(ctx, buildGenerateRequest(a, history, o.loadCustomInstructions(ctx, roomOwnerID)))
	if err != nil {
		log.Printf("ERROR message: generate reply for agent %s: %v", agentID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, fmt.Sprintf("the provider call failed: %v", err))
		return
	}

	reply, err := o.messages.CreateAgent(ctx, triggering.RoomID, agentID, resp.Content, triggering.ID, &resp.InputTokens, &resp.OutputTokens)
	if err != nil {
		log.Printf("ERROR message: store generated reply for agent %s: %v", agentID, err)
		o.fail(ctx, triggering.RoomID, agentID, triggering.ID, "the reply was generated but couldn't be saved.")
		return
	}
	o.hub.Publish(triggering.RoomID, realtime.NewChatMessage(toChatMessage(reply)))
	o.setStatus(ctx, triggering.RoomID, agentID, agent.StatusAvailable)
}

// fail inserts and publishes a visible failure message — ADR-004: a
// failed generation is a real message explaining what happened, never a
// silent drop — then resets the agent's status. Framed as coming from
// the agent itself (sender_kind 'agent', same as a real reply) so it
// renders in the same place a reply would have, rather than as some
// separate system-message concept this phase doesn't build.
func (o *Orchestrator) fail(ctx context.Context, roomID, agentID uuid.UUID, replyToMessageID uuid.UUID, reason string) {
	content := "I couldn't generate a reply — " + reason
	msg, err := o.messages.CreateAgent(ctx, roomID, agentID, content, replyToMessageID, nil, nil)
	if err != nil {
		log.Printf("ERROR message: store failure message for agent %s: %v", agentID, err)
	} else {
		o.hub.Publish(roomID, realtime.NewChatMessage(toChatMessage(msg)))
	}
	o.setStatus(ctx, roomID, agentID, agent.StatusAvailable)
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

// loadCustomInstructions returns roomOwnerID's custom instructions
// (ADR-005), or "" if there's no owner, none are set, or the lookup
// fails. This is a soft dependency, not a hard requirement: a human's
// @mention is waiting on a real reply either way, and custom
// instructions are a nice-to-have refinement of that reply's tone, not
// something worth failing the whole generation over if this one lookup
// has a transient problem.
func (o *Orchestrator) loadCustomInstructions(ctx context.Context, roomOwnerID *uuid.UUID) string {
	if roomOwnerID == nil {
		return ""
	}
	owner, err := o.users.GetByID(ctx, *roomOwnerID)
	if err != nil {
		log.Printf("ERROR message: load owner %s for custom instructions: %v", *roomOwnerID, err)
		return ""
	}
	if owner.CustomInstructions == nil {
		return ""
	}
	return *owner.CustomInstructions
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
func buildGenerateRequest(a agent.Agent, history []Message, customInstructions string) provider.GenerateRequest {
	systemPrompt := fmt.Sprintf(
		"You are %s, an AI agent participating in a chat room in Harmonia, a tool for coordinating work between humans and AI agents. Respond naturally and helpfully to the conversation below.",
		a.Name,
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
