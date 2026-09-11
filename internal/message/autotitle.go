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

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// platformGenerationTimeout bounds one platform-level generation call —
// title (below) or objective (autoobjective.go), both scoped to the
// room owner rather than any one agent. Short — each is one tiny
// completion, not a full conversational reply — but not so short that a
// slightly slow provider round trip fails needlessly.
const platformGenerationTimeout = 30 * time.Second

// maxGeneratedTitleLen caps a generated title's length as a safety net
// against a model ignoring the "short" instruction — long enough for a
// real title, short enough to render cleanly in the sidebar and header.
const maxGeneratedTitleLen = 80

// platformProviderPreference is the fixed order a platform-level
// generation job (TitleGenerator, ObjectiveGenerator) tries the room
// owner's connected BYOK credentials in, when more than one provider is
// connected. Judgment call, stated plainly since the brief asks for it:
// Anthropic first, then OpenAI. Neither job is scoped to a single
// agent.Provider the way Orchestrator's own resolveClient is (a fresh
// room may have no agents registered yet — that's the whole reason
// these need their own resolution, not a mentioned agent's own
// provider), so some fixed order is unavoidable. Anthropic is this
// codebase's own first-listed, first-class provider throughout
// (agent.Provider's own const order, credentials' default client
// order) — reusing that existing precedent here rather than inventing
// a new one (e.g. cost-based or most-recently-connected) that this
// codebase has no infrastructure to actually measure yet.
var platformProviderPreference = []agent.Provider{agent.ProviderAnthropic, agent.ProviderOpenAI}

// TitleGenerator generates a room's title from its first message,
// asynchronously — same shape as Orchestrator (a goroutine with real
// panic recovery, launched off the request path), deliberately not the
// same code path: this is a platform-level utility call scoped to the
// room's owner, not an agent's own conversational reply, and conflating
// the two would tangle "which BYOK credential" logic that's genuinely
// different (a specific agent's provider vs. "whichever the owner has
// connected") into one already-nontrivial type.
type TitleGenerator struct {
	rooms             *room.Store
	credentials       *credentials.Store
	users             *user.Store
	hub               realtime.Publisher
	newProviderClient newProviderClientFunc
}

func NewTitleGenerator(rooms *room.Store, creds *credentials.Store, users *user.Store, hub realtime.Publisher) *TitleGenerator {
	return &TitleGenerator{rooms: rooms, credentials: creds, users: users, hub: hub, newProviderClient: newProviderClient}
}

// GenerateTitle launches, in a new goroutine, generation of roomID's
// title from firstMessageContent. Returns immediately. ownerID is the
// room's owner as already resolved by the caller (the message handler
// already loaded the room to check ownership; passed through rather
// than re-fetched here, same convention as Orchestrator.TriggerReply).
//
// A panic here must never crash the process — recovered and logged,
// same posture as Orchestrator. Unlike a failed reply, a failed title
// generation has no ADR-004 requirement to surface a visible failure
// message: this is a cosmetic convenience, not something a human
// @mentioned and is waiting on an answer from. A failure just leaves
// the room showing room.PlaceholderName, which is a perfectly fine,
// honest state — not silently broken, just untitled.
func (t *TitleGenerator) GenerateTitle(roomID uuid.UUID, ownerID *uuid.UUID, firstMessageContent string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), platformGenerationTimeout)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ERROR message: panic generating title for room %s: %v", roomID, r)
			}
		}()
		t.generate(ctx, roomID, ownerID, firstMessageContent)
	}()
}

func (t *TitleGenerator) generate(ctx context.Context, roomID uuid.UUID, ownerID *uuid.UUID, firstMessageContent string) {
	client, err := resolvePlatformProviderClient(ctx, t.credentials, ownerID, t.newProviderClient)
	if err != nil {
		log.Printf("ERROR message: resolve provider client for room %s title: %v", roomID, err)
		return
	}

	resp, err := client.Generate(ctx, buildTitleRequest(firstMessageContent, loadOwnerCustomInstructions(ctx, t.users, ownerID)))
	if err != nil {
		log.Printf("ERROR message: generate title for room %s: %v", roomID, err)
		return
	}
	title := sanitizeTitle(resp.Content)
	if title == "" {
		log.Printf("ERROR message: generated title for room %s was empty after sanitizing", roomID)
		return
	}

	// The race guard ADR-004 requires: re-read the room's current name
	// immediately before writing, not whatever it was when this job
	// started. A human's manual rename — arbitrarily faster than this
	// round trip through a provider — must win unconditionally. Skipping
	// silently here (no error, no log-as-ERROR) is deliberate: losing
	// this race isn't a failure, it's the guard working as designed.
	current, err := t.rooms.GetByID(ctx, roomID)
	if err != nil {
		log.Printf("ERROR message: reload room %s before applying generated title: %v", roomID, err)
		return
	}
	if current.Name != room.PlaceholderName {
		log.Printf("message: room %s was renamed before title generation finished; discarding generated title %q", roomID, title)
		return
	}

	updated, err := t.rooms.Update(ctx, roomID, &title, nil, nil, nil, nil)
	if err != nil {
		log.Printf("ERROR message: apply generated title for room %s: %v", roomID, err)
		return
	}
	t.hub.Publish(roomID, realtime.NewRoomRenamedMessage(updated.ID, updated.Name))
}

// resolvePlatformProviderClient resolves a provider client from one of
// ownerID's connected BYOK credentials, trying platformProviderPreference
// in order, then falling back to the same env vars the Orchestrator's
// own dev-path fallback uses, in the same preference order. Shared by
// every platform-level generation job (TitleGenerator, ObjectiveGenerator
// in autoobjective.go) — neither is scoped to a single agent.Provider the
// way Orchestrator's own resolveClient is, so both need this
// owner-preference-order resolution instead, and it's genuinely the same
// logic for both, not merely similar.
func resolvePlatformProviderClient(ctx context.Context, creds *credentials.Store, ownerID *uuid.UUID, newProviderClient newProviderClientFunc) (provider.Agent, error) {
	for _, p := range platformProviderPreference {
		client, err := creds.Resolve(ctx, ownerID, p)
		if err == nil {
			return client, nil
		}
		if !errors.Is(err, credentials.ErrNoCredential) && !errors.Is(err, credentials.ErrEncryptionNotConfigured) {
			return nil, err
		}
	}
	for _, p := range platformProviderPreference {
		envKey := envKeyFor(p)
		if envKey == "" {
			continue
		}
		if apiKey := os.Getenv(envKey); apiKey != "" {
			return newProviderClient(p, apiKey)
		}
	}
	return nil, fmt.Errorf("message: no provider credential available for this platform-level generation call")
}

// buildTitleRequest asks for a short, plain title with no framing
// around it — a system prompt this narrow is appropriate here in a way
// it wouldn't be for a real conversational reply, since the only
// output this call has any use for is the bare title string itself.
// customInstructions is prepended the same way Orchestrator's
// buildGenerateRequest does, so a user's stated preferences (e.g.
// "always title things in French") apply here too — same source of
// truth, same context-assembly point as a real reply.
func buildTitleRequest(firstMessageContent, customInstructions string) provider.GenerateRequest {
	systemPrompt := "Generate a short title (3 to 6 words) summarizing the topic of the message below. " +
		"Reply with only the title itself — no quotation marks, no trailing punctuation, no preamble."
	if customInstructions != "" {
		systemPrompt = customInstructions + "\n\n" + systemPrompt
	}
	return provider.GenerateRequest{
		SystemPrompt: systemPrompt,
		Messages:     []provider.Message{{Role: "user", Content: firstMessageContent}},
	}
}

// loadOwnerCustomInstructions mirrors Orchestrator.loadOwnerContext's own
// custom-instructions half — shared by TitleGenerator and
// ObjectiveGenerator (autoobjective.go), both platform-level utility
// calls scoped to the room owner rather than any one agent. Same
// soft-fail posture as Orchestrator's own version: a lookup failure logs
// and falls back to no instructions rather than failing generation,
// which has no ADR-004 requirement to surface a visible failure.
func loadOwnerCustomInstructions(ctx context.Context, users *user.Store, ownerID *uuid.UUID) string {
	if ownerID == nil {
		return ""
	}
	owner, err := users.GetByID(ctx, *ownerID)
	if err != nil {
		log.Printf("ERROR message: load owner %s for generation custom instructions: %v", *ownerID, err)
		return ""
	}
	if owner.CustomInstructions == nil {
		return ""
	}
	return *owner.CustomInstructions
}

// sanitizeTitle strips the surrounding quotes/punctuation a model adds
// despite being asked not to, and caps length as a safety net — the
// same "don't fully trust generated output's shape" posture as
// Orchestrator trusting Generate's content directly only because a
// chat reply has no shape constraints a title does.
func sanitizeTitle(raw string) string {
	title := strings.TrimSpace(raw)
	title = strings.Trim(title, `"'`)
	title = strings.TrimRight(title, ".!")
	title = strings.TrimSpace(title)
	if len(title) > maxGeneratedTitleLen {
		title = strings.TrimSpace(title[:maxGeneratedTitleLen])
	}
	return title
}
