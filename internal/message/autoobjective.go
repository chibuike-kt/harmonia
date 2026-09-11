package message

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// maxGeneratedObjectiveLen caps a generated objective's length as a
// safety net against a model ignoring the "short" instruction — longer
// than maxGeneratedTitleLen (this is a sentence or two, not a title),
// short enough to render cleanly in RoomInfoPanel's own Objective section.
const maxGeneratedObjectiveLen = 280

// objectiveGenerationThreshold is how many messages a room needs before
// ObjectiveGenerator fires — not 1, the way the auto-title job fires on
// the room's very first message: a single message is exactly the
// "oldest message" stand-in this replaces, with no more information in
// it than that stand-in already had. Waiting for a few messages gives
// the job an actual exchange to summarize (typically at least one human
// turn and one reply) without waiting so long that a room sits without
// a real objective for most of its early life. Checked the same way the
// title trigger is — CreateHandler's own post-commit message count —
// so this fires exactly once, on whichever message first brings the
// room to this count.
const objectiveGenerationThreshold = 3

// objectiveEarlyMessageCap bounds how many of the room's early messages
// get sent to the summarization call. ListByRoom returns oldest-first,
// so capping to the first few here keeps this honestly about the room's
// beginning — by the time this async job's provider round trip actually
// completes, the room may well have grown past objectiveGenerationThreshold,
// and this must not balloon into summarizing an ever-growing transcript.
const objectiveEarlyMessageCap = 10

// ObjectiveGenerator generates a room's objective from its early
// messages, asynchronously — the same platform-level-utility shape as
// TitleGenerator (see autotitle.go), sharing that type's own BYOK
// owner-credential resolution (resolvePlatformProviderClient) and custom
// instructions loading (loadOwnerCustomInstructions) rather than
// duplicating either. Kept as its own type, not folded into
// TitleGenerator, for the same reason TitleGenerator itself is separate
// from Orchestrator: this needs its own message history (the early
// window, not the first message alone), so it holds its own *Store
// dependency TitleGenerator has no use for.
type ObjectiveGenerator struct {
	rooms             *room.Store
	messages          *Store
	credentials       *credentials.Store
	users             *user.Store
	hub               realtime.Publisher
	newProviderClient newProviderClientFunc
}

func NewObjectiveGenerator(rooms *room.Store, messages *Store, creds *credentials.Store, users *user.Store, hub realtime.Publisher) *ObjectiveGenerator {
	return &ObjectiveGenerator{rooms: rooms, messages: messages, credentials: creds, users: users, hub: hub, newProviderClient: newProviderClient}
}

// GenerateObjective launches, in a new goroutine, generation of roomID's
// objective from its early messages. Returns immediately. ownerID is the
// room's owner as already resolved by the caller — same convention as
// TitleGenerator.GenerateTitle and Orchestrator.TriggerReply.
//
// A panic here must never crash the process — recovered and logged, same
// posture as TitleGenerator. A failed generation has no ADR-004
// requirement to surface a visible failure message: this is a cosmetic
// convenience, not something a human is waiting on an answer from. A
// failure just leaves the room's objective unset, which RoomInfoPanel
// renders as a plain empty state — not silently broken, just not
// summarized yet.
func (o *ObjectiveGenerator) GenerateObjective(roomID uuid.UUID, ownerID *uuid.UUID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), platformGenerationTimeout)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("ERROR message: panic generating objective for room %s: %v", roomID, r)
			}
		}()
		o.generate(ctx, roomID, ownerID)
	}()
}

func (o *ObjectiveGenerator) generate(ctx context.Context, roomID uuid.UUID, ownerID *uuid.UUID) {
	early, err := o.loadEarlyMessages(ctx, roomID)
	if err != nil {
		log.Printf("ERROR message: load early messages for room %s objective: %v", roomID, err)
		return
	}
	if len(early) == 0 {
		return
	}

	client, err := resolvePlatformProviderClient(ctx, o.credentials, ownerID, o.newProviderClient)
	if err != nil {
		log.Printf("ERROR message: resolve provider client for room %s objective: %v", roomID, err)
		return
	}

	resp, err := client.Generate(ctx, buildObjectiveRequest(early, loadOwnerCustomInstructions(ctx, o.users, ownerID)))
	if err != nil {
		log.Printf("ERROR message: generate objective for room %s: %v", roomID, err)
		return
	}
	objective := sanitizeObjective(resp.Content)
	if objective == "" {
		log.Printf("ERROR message: generated objective for room %s was empty after sanitizing", roomID)
		return
	}

	// The race guard: re-read the room's current objective immediately
	// before writing, not whatever it was when this job started. A
	// human's manual edit — arbitrarily faster than this round trip
	// through a provider — must win unconditionally, identical in spirit
	// to TitleGenerator's own guard against PlaceholderName, just keyed
	// off nil instead of a sentinel string (see room.Room.Objective's own
	// doc comment for why). Skipping silently here (no error, no
	// log-as-ERROR) is deliberate: losing this race isn't a failure, it's
	// the guard working as designed.
	current, err := o.rooms.GetByID(ctx, roomID)
	if err != nil {
		log.Printf("ERROR message: reload room %s before applying generated objective: %v", roomID, err)
		return
	}
	if current.Objective != nil {
		log.Printf("message: room %s already had an objective set before generation finished; discarding generated objective %q", roomID, objective)
		return
	}

	updated, err := o.rooms.Update(ctx, roomID, nil, nil, nil, nil, &objective)
	if err != nil {
		log.Printf("ERROR message: apply generated objective for room %s: %v", roomID, err)
		return
	}
	o.hub.Publish(roomID, realtime.NewRoomObjectiveMessage(updated.ID, *updated.Objective))
}

// loadEarlyMessages loads roomID's message history and caps it to the
// room's actual beginning — see objectiveEarlyMessageCap's own comment
// for why this can't just trust ListByRoom's full result once the room
// has grown past objectiveGenerationThreshold by the time this async
// call actually runs.
func (o *ObjectiveGenerator) loadEarlyMessages(ctx context.Context, roomID uuid.UUID) ([]Message, error) {
	history, err := o.messages.ListByRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if len(history) > objectiveEarlyMessageCap {
		history = history[:objectiveEarlyMessageCap]
	}
	return history, nil
}

// buildObjectiveRequest asks for a short plain-prose objective summary,
// with the room's early messages folded into one transcript-style user
// turn rather than played back as separate provider.Message turns —
// unlike Orchestrator's buildGenerateRequest, there's no single agent
// perspective here to cast either side as "assistant," so a labeled
// transcript is the honest shape for what's actually being asked: an
// outside summary of a conversation, not a continuation of it.
// customInstructions is prepended the same way buildTitleRequest does,
// so a user's stated preferences apply here too.
func buildObjectiveRequest(early []Message, customInstructions string) provider.GenerateRequest {
	systemPrompt := "Summarize the objective of the conversation below in one or two short sentences — what is this room trying to accomplish? " +
		"Reply with only the summary itself — no preamble, no quotation marks."
	if customInstructions != "" {
		systemPrompt = customInstructions + "\n\n" + systemPrompt
	}
	var transcript strings.Builder
	for _, m := range early {
		speaker := "Human"
		if m.SenderKind == SenderAgent {
			speaker = "Agent"
		}
		fmt.Fprintf(&transcript, "%s: %s\n", speaker, m.Content)
	}
	return provider.GenerateRequest{
		SystemPrompt: systemPrompt,
		Messages:     []provider.Message{{Role: "user", Content: transcript.String()}},
	}
}

// sanitizeObjective strips the surrounding quotes a model adds despite
// being asked not to, and caps length as a safety net — the same
// "don't fully trust generated output's shape" posture as sanitizeTitle.
// Unlike sanitizeTitle, trailing punctuation is left alone: an objective
// is a real sentence, not a title, so a trailing period is correct
// output, not something to strip.
func sanitizeObjective(raw string) string {
	objective := strings.TrimSpace(raw)
	objective = strings.Trim(objective, `"'`)
	objective = strings.TrimSpace(objective)
	if len(objective) > maxGeneratedObjectiveLen {
		objective = strings.TrimSpace(objective[:maxGeneratedObjectiveLen])
	}
	return objective
}
