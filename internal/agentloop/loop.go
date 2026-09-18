package agentloop

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chibuike-kt/harmonia/internal/companionrelay"
	"github.com/chibuike-kt/harmonia/internal/cost"
	"github.com/chibuike-kt/harmonia/internal/message"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
)

const (
	eventLoopStarted   = "AGENT_LOOP_STARTED"
	eventLoopStopped   = "AGENT_LOOP_STOPPED"
	eventLoopCompleted = "AGENT_LOOP_COMPLETED"

	relayTimeout          = 20 * time.Second
	runCommandTimeout     = 180 * time.Second
	createTerminalTimeout = 15 * time.Second
	narrateTimeout        = 10 * time.Second
)

// run is a Session's entire real lifetime: create its dedicated terminal,
// then cycle tool-call -> observe -> decide -> tool-call again until one
// of ADR-011's real stopping conditions fires. Always exits through
// finish, exactly once, so State/Message land in a consistent place
// regardless of which condition stopped it.
func (m *Manager) run(ctx context.Context, s *Session) {
	defer m.forget(s)

	terminalID, err := m.createSessionTerminal(ctx, s)
	if err != nil {
		s.finish(StateFailed, "failed to open a real terminal for this session: "+err.Error())
		m.recordAndPublish(s)
		return
	}
	s.setTerminalID(terminalID)

	m.recordEvent(s, eventLoopStarted, map[string]any{
		"task": s.Task, "max_cycles": s.Bounds.MaxCycles,
		"dollar_cap_usd": s.Bounds.DollarCapUSD, "wall_clock_seconds": int(s.Bounds.WallClock.Seconds()),
	})
	m.narrate(s, fmt.Sprintf(
		"session started — task: %s (max %d cycles, $%.2f cap, %s wall-clock limit)",
		s.Task, s.Bounds.MaxCycles, s.Bounds.DollarCapUSD, s.Bounds.WallClock,
	))
	m.publishStatus(s)

	tools := sessionTools()
	messages := []provider.Message{{Role: "user", Content: s.Task}}

	for {
		present, presenceErr := realtime.IsHumanPresent(ctx, m.rdb, s.RoomID)
		if presenceErr != nil || !present {
			// Silent and immediate, per the ADR: this is the safety
			// mechanism working correctly, not a failure worth narrating.
			s.finish(StateStoppedPresence, "")
			m.recordAndPublish(s)
			return
		}
		if s.incrementCycle() > s.Bounds.MaxCycles {
			s.finish(StateStoppedBound, fmt.Sprintf("stopped: reached the configured limit of %d tool-call cycles", s.Bounds.MaxCycles))
			m.narrate(s, s.Message)
			m.recordAndPublish(s)
			return
		}
		if snap := s.snapshot(); snap.SpendUSD >= s.Bounds.DollarCapUSD {
			s.finish(StateStoppedBound, fmt.Sprintf("stopped: reached the configured $%.2f cost cap (spent $%.4f)", s.Bounds.DollarCapUSD, snap.SpendUSD))
			m.narrate(s, s.Message)
			m.recordAndPublish(s)
			return
		}
		if time.Now().After(s.deadline) {
			s.finish(StateStoppedBound, fmt.Sprintf("stopped: reached the configured %s wall-clock limit", s.Bounds.WallClock))
			m.narrate(s, s.Message)
			m.recordAndPublish(s)
			return
		}
		if ctx.Err() != nil {
			s.finish(StateStoppedManual, "stopped: a human ended this session")
			m.narrate(s, s.Message)
			m.recordAndPublish(s)
			return
		}

		allTools := append(append([]provider.ToolDef{}, tools...), m.createTaskTool(ctx, s)...)
		resp, err := s.client.Generate(ctx, provider.GenerateRequest{
			SystemPrompt:    systemPrompt(s),
			Messages:        messages,
			Tools:           allTools,
			RequireToolCall: true,
		})
		if err != nil {
			if ctx.Err() != nil {
				s.finish(StateStoppedManual, "stopped: a human ended this session")
				m.narrate(s, s.Message)
				m.recordAndPublish(s)
				return
			}
			s.finish(StateFailed, "generation failed: "+err.Error())
			m.narrate(s, "[error] "+err.Error())
			m.recordAndPublish(s)
			return
		}
		s.addSpend(cost.EstimateUSD(string(s.Provider), resp.InputTokens, resp.OutputTokens))
		m.publishStatus(s)

		if len(resp.ToolCalls) == 0 {
			// RequireToolCall should make this unreachable in practice;
			// handled defensively rather than treated as a hard failure —
			// the assistant's own text is kept in history and the loop
			// tries again next cycle, still under the same real bounds.
			messages = append(messages, provider.Message{Role: "assistant", Content: resp.Content})
			m.narrate(s, fmt.Sprintf("cycle %d/%d — no tool call in the response, retrying", s.Cycle, s.Bounds.MaxCycles))
			continue
		}

		call := resp.ToolCalls[0]
		messages = append(messages, provider.Message{Role: "assistant", Content: resp.Content, ToolCalls: []provider.ToolCall{call}})

		if call.Name == toolMarkDone {
			summary, _ := call.Input["summary"].(string)
			s.finish(StateCompleted, summary)
			m.narrate(s, fmt.Sprintf("cycle %d/%d — mark_done: %s", s.Cycle, s.Bounds.MaxCycles, summary))
			m.recordEvent(s, eventLoopCompleted, map[string]any{"summary": summary, "cycles": s.Cycle})
			m.publishStatus(s)
			return
		}

		m.narrate(s, fmt.Sprintf("cycle %d/%d — %s(%s)", s.Cycle, s.Bounds.MaxCycles, call.Name, summarizeInput(call.Input)))
		output, toolErr := m.dispatchTool(ctx, s, call)
		result := output
		if toolErr != nil {
			if ctx.Err() != nil {
				s.finish(StateStoppedManual, "stopped: a human ended this session")
				m.narrate(s, s.Message)
				m.recordAndPublish(s)
				return
			}
			result = "error: " + toolErr.Error()
		}
		m.narrate(s, fmt.Sprintf("cycle %d/%d — %s result: %s", s.Cycle, s.Bounds.MaxCycles, call.Name, truncate(result, 500)))
		messages = append(messages, provider.Message{Role: "tool", ToolCallID: call.ID, Content: result})
	}
}

// systemPrompt bakes ADR-011's self-verification requirement directly
// into the model's own framing for this session (build brief item 4:
// "state how you built this expectation into the prompting") — paired
// with mark_done's own tool description, which repeats and enforces the
// same requirement at the one point the model could otherwise skip it.
func systemPrompt(s *Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are working a sustained, autonomous session inside Harmonia's IDE on a real local project.\n\n")
	fmt.Fprintf(&b, "Task: %s\n\n", s.Task)
	b.WriteString("You have real tools: read_file, write_file, and run_command (a real shell in your own dedicated terminal), " +
		"plus create_task to track real follow-on work in this room. Work through this task step by step — read what you " +
		"need, make real changes, run real commands to check your own work.\n\n")
	b.WriteString("Before you call mark_done, you must have actually run this project's real build, test, and/or lint " +
		"commands via run_command in this same session and seen them pass. Do not declare the task done from assumption, " +
		"and never just from re-reading your own changes — real verification only. If there is genuinely nothing to " +
		"build, test, or lint for this task, say so explicitly in your mark_done summary and explain why.\n\n")
	fmt.Fprintf(&b, "This session is bounded: at most %d tool-call cycles, a $%.2f cost cap, and a %s wall-clock limit. "+
		"It also stops immediately, with no warning, the instant the human watching this session stops watching — if "+
		"that happens mid-task, there is nothing more for you to do.\n\n", s.Bounds.MaxCycles, s.Bounds.DollarCapUSD, s.Bounds.WallClock)
	b.WriteString("Call exactly one tool now to make real progress, or call mark_done if — and only if — the task is genuinely complete and verified.")
	return b.String()
}

// createTaskTool builds create_task's tool definition against this room's
// real currently-open tasks — refetched every cycle (not cached at
// session start) so the description a model sees always reflects real,
// current room state, the same freshness the ordinary chat orchestrator's
// own per-turn call already gets.
func (m *Manager) createTaskTool(ctx context.Context, s *Session) []provider.ToolDef {
	active, err := m.tasks.ListActiveByRoom(ctx, s.RoomID)
	if err != nil {
		return nil
	}
	return []provider.ToolDef{message.CreateTaskTool(active)}
}

// dispatchTool runs one real tool call to completion — every case here is
// either a real companion relay round trip (read_file/write_file/
// run_command, ADR-010's own protocol) or create_task's own already-real,
// already-immediate execution (ADR-006 batch C, reused unchanged).
func (m *Manager) dispatchTool(ctx context.Context, s *Session, call provider.ToolCall) (string, error) {
	switch call.Name {
	case toolReadFile:
		path, _ := call.Input["path"].(string)
		if path == "" {
			return "", errors.New("read_file: path is required")
		}
		res, err := companionrelay.Dispatch(ctx, m.relay, m.hub, s.RoomID, "read_file", path, "", relayTimeout)
		if err != nil {
			return "", err
		}
		if res.Err != "" {
			return "", errors.New(res.Err)
		}
		return res.Output, nil

	case toolWriteFile:
		path, _ := call.Input["path"].(string)
		content, _ := call.Input["content"].(string)
		if path == "" {
			return "", errors.New("write_file: path is required")
		}
		data := base64.StdEncoding.EncodeToString([]byte(content))
		res, err := companionrelay.Dispatch(ctx, m.relay, m.hub, s.RoomID, "write_file", path, data, relayTimeout)
		if err != nil {
			return "", err
		}
		if res.Err != "" {
			return "", errors.New(res.Err)
		}
		return "written", nil

	case toolRunCommand:
		command, _ := call.Input["command"].(string)
		if command == "" {
			return "", errors.New("run_command: command is required")
		}
		res, err := companionrelay.Dispatch(ctx, m.relay, m.hub, s.RoomID, "run_command", s.terminalID(), command, runCommandTimeout)
		if err != nil {
			return "", err
		}
		if res.Err != "" {
			return "", errors.New(res.Err)
		}
		return res.Output, nil

	case toolCreateTask:
		message.ExecuteCreateTask(ctx, m.beginner, m.hub, s.RoomID, s.AgentID, call.Input)
		objective, _ := call.Input["objective"].(string)
		return "created task: " + objective, nil

	default:
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
}

// createSessionTerminal dispatches a real create_terminal companion
// action to get this session its own dedicated real terminal — every
// run_command call, and every narration line, targets this one terminal
// for the session's whole lifetime, per Batch A item 5's "reuse the
// existing narration pattern exactly."
func (m *Manager) createSessionTerminal(ctx context.Context, s *Session) (string, error) {
	res, err := companionrelay.Dispatch(ctx, m.relay, m.hub, s.RoomID, "create_terminal", "", "", createTerminalTimeout)
	if err != nil {
		return "", err
	}
	if res.Err != "" {
		return "", errors.New(res.Err)
	}
	if res.Output == "" {
		return "", errors.New("companion returned no terminal id")
	}
	return res.Output, nil
}

// narrate writes one real line into this session's own dedicated terminal
// via the existing terminal_narrate mechanism (internal/companion's own
// narrateTerminal, unchanged) — a bracketed tag plus real ANSI styling,
// the same convention every other agent narration in this terminal uses.
// Uses its own background context and short timeout, deliberately
// decoupled from the session's own (possibly already-canceled) ctx: the
// whole point is that a session's final "stopped" line must still render
// even after a human has just hit Stop.
func (m *Manager) narrate(s *Session, text string) {
	line := "\x1b[1;36m[agent loop]\x1b[0m " + text
	_, _ = companionrelay.Dispatch(context.Background(), m.relay, m.hub, s.RoomID, "terminal_narrate", s.terminalID(), line, narrateTimeout)
}

func (m *Manager) publishStatus(s *Session) {
	snap := s.snapshot()
	m.hub.Publish(s.RoomID, realtime.NewAgentLoopStatusMessage(realtime.AgentLoopStatus{
		SessionID: s.ID, RoomID: s.RoomID, Cycle: snap.Cycle, MaxCycles: s.Bounds.MaxCycles,
		SpendUSD: snap.SpendUSD, DollarCapUSD: s.Bounds.DollarCapUSD,
		State: string(snap.State), Message: snap.Message,
	}))
}

// recordEvent appends one real, durable audit-trail entry for this
// session's lifecycle — event.Store.Record, unchanged, the same
// append-only mechanism every other real event in this codebase uses.
func (m *Manager) recordEvent(s *Session, eventType string, payload map[string]any) {
	payload["session_id"] = s.ID.String()
	_ = m.events.Record(context.Background(), s.RoomID, nil, &s.AgentID, eventType, payload)
}

// recordAndPublish is finish's own real audit trail: exactly one
// AGENT_LOOP_STOPPED event per session, carrying its own real terminal
// state and reason, plus the matching live status publish so every open
// tab reflects it without polling.
func (m *Manager) recordAndPublish(s *Session) {
	snap := s.snapshot()
	m.recordEvent(s, eventLoopStopped, map[string]any{"state": string(snap.State), "message": snap.Message, "cycles": snap.Cycle, "spend_usd": snap.SpendUSD})
	m.publishStatus(s)
}

func summarizeInput(input map[string]any) string {
	parts := make([]string, 0, len(input))
	for k, v := range input {
		parts = append(parts, fmt.Sprintf("%s=%v", k, truncate(fmt.Sprint(v), 80)))
	}
	return strings.Join(parts, ", ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
