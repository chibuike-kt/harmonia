package message

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/credentials"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/room"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
	"github.com/chibuike-kt/harmonia/internal/user"
)

// attachmentRequestJSON builds a createRequest's own attachment JSON —
// encoding/json base64-encodes a []byte field automatically, so this
// mirrors exactly what Composer.tsx's own FileReader-derived payload
// produces, not a hand-rolled shortcut.
func attachmentRequestJSON(t *testing.T, content []byte, filename, mimeType string) string {
	t.Helper()
	b, err := json.Marshal(attachmentRequest{Content: content, Filename: filename, MimeType: mimeType})
	if err != nil {
		t.Fatalf("marshal attachment: %v", err)
	}
	return string(b)
}

// TestIntegration_CreateHandler_AttachmentReachesGenerate is ADR-008
// batch A's own central proof: a real attachment on a human message
// survives storage and comes back out the other side as a real
// provider.Attachment on the exact provider.Message the reply generation
// actually used — not just that the request didn't error. Checks three
// independent things: the stored row's own bytes (direct DB read, not
// trusting the app's own read path to catch a write bug), the JSON
// response's filename/mime_type (and the deliberate absence of raw
// content — see message.Message.AttachmentContent's own doc comment),
// and the captured GenerateRequest's Attachment field.
func TestIntegration_CreateHandler_AttachmentReachesGenerate(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-attach-")
	rm, err := rooms.Create(ctx, &owner.ID, "attachment-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	a, err := agents.Register(ctx, rm.ID, "Claude", agent.ProviderAnthropic, nil, "hash-attach")
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
		return &fakeProviderAgent{content: "The retry loop looks fine to me.", capturedRequest: &captured}, nil
	}
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, rec, orch, titleGen, objectiveGen)

	const fileText = "func retry() error {\n\treturn nil\n}\n"
	fileBytes := []byte(fileText)

	body := `{"content":"@Claude does this retry logic look right?","mentioned_agent_ids":[` +
		`"` + a.ID.String() + `"],"attachment":` +
		attachmentRequestJSON(t, fileBytes, "retry.go", "text/plain") + `}`

	httpRec := doCreateMessage(h, owner, rm.ID.String(), body)
	if httpRec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", httpRec.Code, http.StatusCreated, httpRec.Body.String())
	}

	// The JSON response carries filename/mime_type but never raw content —
	// checked against the decoded map, not just the typed struct, so a
	// regression that accidentally adds a json tag to AttachmentContent
	// would actually be caught here.
	var raw map[string]any
	if err := json.Unmarshal(httpRec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if raw["attachment_filename"] != "retry.go" {
		t.Fatalf("response attachment_filename = %v, want %q", raw["attachment_filename"], "retry.go")
	}
	if raw["attachment_mime_type"] != "text/plain" {
		t.Fatalf("response attachment_mime_type = %v, want %q", raw["attachment_mime_type"], "text/plain")
	}
	if _, leaked := raw["attachment_content"]; leaked {
		t.Fatalf("response leaked attachment_content over the wire: %v", raw["attachment_content"])
	}

	var mention Message
	if err := json.Unmarshal(httpRec.Body.Bytes(), &mention); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Direct DB read — proving the row itself, not just this process's
	// own in-memory view of what it wrote.
	var storedContent []byte
	var storedFilename, storedMimeType string
	if err := pool.QueryRow(ctx, `
		SELECT attachment_content, attachment_filename, attachment_mime_type
		FROM messages WHERE id = $1
	`, mention.ID).Scan(&storedContent, &storedFilename, &storedMimeType); err != nil {
		t.Fatalf("query stored attachment: %v", err)
	}
	if string(storedContent) != fileText {
		t.Fatalf("stored attachment_content = %q, want %q", storedContent, fileText)
	}
	if storedFilename != "retry.go" || storedMimeType != "text/plain" {
		t.Fatalf("stored filename/mime = %q/%q, want retry.go/text/plain", storedFilename, storedMimeType)
	}

	reply := waitForReplyMessage(t, ctx, s, rm.ID, mention.ID)
	if reply.Content == "" {
		t.Fatalf("expected a real reply, got empty content")
	}

	// The actual proof this test exists for: the GenerateRequest the
	// orchestrator built for this invocation carried the attachment on
	// the triggering message, translated into a real provider.Attachment
	// — not dropped, not stubbed out, not routed around the ordinary
	// recency-window history.
	var found *provider.Message
	for i := range captured.Messages {
		if captured.Messages[i].Attachment != nil {
			found = &captured.Messages[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no message in the captured GenerateRequest carried an attachment; Messages = %+v", captured.Messages)
	}
	if found.Attachment.Filename != "retry.go" {
		t.Fatalf("captured Attachment.Filename = %q, want %q", found.Attachment.Filename, "retry.go")
	}
	if found.Attachment.MimeType != "text/plain" {
		t.Fatalf("captured Attachment.MimeType = %q, want %q", found.Attachment.MimeType, "text/plain")
	}
	if string(found.Attachment.Content) != fileText {
		t.Fatalf("captured Attachment.Content = %q, want %q", found.Attachment.Content, fileText)
	}
	if found.Attachment.Kind() != provider.AttachmentText {
		t.Fatalf("captured Attachment.Kind() = %v, want AttachmentText", found.Attachment.Kind())
	}
}

// TestIntegration_CreateHandler_AttachmentSizeCapRejected proves the
// application-layer half of ADR-008's defense-in-depth size guard —
// migrations/0014_search_and_files.up.sql's own CHECK constraint is the
// backstop, this is the check that's supposed to fire first, before the
// insert is even attempted.
func TestIntegration_CreateHandler_AttachmentSizeCapRejected(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-attach-big-")
	rm, err := rooms.Create(ctx, &owner.ID, "attachment-size-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch, titleGen, objectiveGen)

	oversized := make([]byte, maxAttachmentBytes+1)
	body := `{"content":"here you go","attachment":` +
		attachmentRequestJSON(t, oversized, "big.bin", "application/octet-stream") + `}`

	rec := doCreateMessage(h, owner, rm.ID.String(), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	count, err := s.CountByRoom(ctx, rm.ID)
	if err != nil {
		t.Fatalf("CountByRoom: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the oversized attachment to be rejected before any insert, room has %d messages", count)
	}
}

// TestIntegration_CreateHandler_AttachmentMissingFieldsRejected proves
// the all-or-nothing validation: content/filename/mime_type must all be
// present together, not a partially-formed attachment silently accepted
// with an empty filename or mime type.
func TestIntegration_CreateHandler_AttachmentMissingFieldsRejected(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-attach-partial-")
	rm, err := rooms.Create(ctx, &owner.ID, "attachment-partial-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch, titleGen, objectiveGen)

	// filename omitted entirely.
	body := `{"content":"oops","attachment":{"content":"` +
		base64.StdEncoding.EncodeToString([]byte("hi")) + `","mime_type":"text/plain"}}`

	rec := doCreateMessage(h, owner, rm.ID.String(), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestIntegration_CreateHandler_AttachmentAloneNeedsNoCaption proves a
// human can send a file with no message text — the same allowance a
// real chat product gives "here's a file," not a caption requirement
// left over from before attachments existed.
func TestIntegration_CreateHandler_AttachmentAloneNeedsNoCaption(t *testing.T) {
	pool, rdb := connectMessageTestPool(t)
	ctx := context.Background()

	users := user.NewStore(pool)
	rooms := room.NewStore(pool)
	agents := agent.NewStore(pool)
	creds := credentials.NewStore(pool, nil)
	beginner := store.PoolBeginner{Pool: pool}

	owner := seedMessageTestUser(t, ctx, users, "msg-attach-nocaption-")
	rm, err := rooms.Create(ctx, &owner.ID, "attachment-nocaption-room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	s := NewStore(pool)
	tasks := task.NewStore(pool)
	orch := NewOrchestrator(s, agents, creds, users, rooms, tasks, beginner, realtime.NewHub(), rdb)
	titleGen := NewTitleGenerator(rooms, creds, users, realtime.NewHub())
	objectiveGen := NewObjectiveGenerator(rooms, s, creds, users, realtime.NewHub())
	h := s.CreateHandler(rooms, agents, beginner, realtime.NewHub(), orch, titleGen, objectiveGen)

	body := `{"content":"","attachment":` +
		attachmentRequestJSON(t, []byte("just a file, no caption"), "note.txt", "text/plain") + `}`

	rec := doCreateMessage(h, owner, rm.ID.String(), body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
}
