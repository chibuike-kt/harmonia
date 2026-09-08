package message

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"github.com/chibuike-kt/harmonia/internal/actionproposal"
	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/event"
	"github.com/chibuike-kt/harmonia/internal/protocol"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/realtime"
	"github.com/chibuike-kt/harmonia/internal/store"
	"github.com/chibuike-kt/harmonia/internal/task"
)

// ADR-006 batch C's two real tools. Both go through the exact same
// native tool-use mechanism batch B's mention_agent already proved
// (single-turn, no result fed back) — this file adds no new plumbing to
// provider.GenerateRequest/Response, only new tool definitions and what
// happens when the model calls one.
const (
	createTaskToolName     = "create_task"
	requestHandoffToolName = "request_handoff"
)

// createTaskTool is always offered — create_task is low-stakes and
// already an ordinary, frequent operation in this system (ADR-006), so
// unlike mention_agent it isn't gated behind any room setting.
func createTaskTool() provider.ToolDef {
	return provider.ToolDef{
		Name: createTaskToolName,
		Description: "Create a new task in this room. Executes immediately " +
			"— the same as a human posting it directly — so only call this " +
			"for real, actionable work, not to narrate what you're already doing.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objective": map[string]any{
					"type":        "string",
					"description": "A concise, actionable statement of what the task is.",
				},
			},
			"required": []string{"objective"},
		},
	}
}

// requestHandoffTool is only offered when the room has both at least one
// active (non-terminal) task and at least one other agent — task_id and
// to_agent_name are each validated against exactly those sets, so
// offering the tool with nothing real for either to reference would just
// invite a call executeRequestHandoff can only ever drop. Both sets are
// embedded directly in the description — the model can only ever supply
// a task_id or agent name we already handed it, never one it invented,
// the same non-leaking resolution discipline resolveMentionToolCalls
// already applies. This is unlike mention_agent, which can rely on a
// human having already used an agent's name in the visible conversation
// — request_handoff has no such guarantee (a room can go straight from
// one agent's first reply to proposing a handoff, with the target named
// nowhere in the history yet), so the agent roster has to be stated
// here explicitly rather than assumed.
func requestHandoffTool(activeTasks []task.Task, otherAgents []agent.Agent) provider.ToolDef {
	var tasksList strings.Builder
	for _, t := range activeTasks {
		fmt.Fprintf(&tasksList, "\n- %s: %s", t.ID, t.Objective)
	}
	var agentsList strings.Builder
	for _, a := range otherAgents {
		fmt.Fprintf(&agentsList, "\n- %s", a.Name)
	}
	return provider.ToolDef{
		Name: requestHandoffToolName,
		Description: "Propose handing off one of this room's open tasks to " +
			"another agent already in this room. This does not execute " +
			"immediately — it creates a pending proposal a human must " +
			"approve before the real handoff happens. Only call this when a " +
			"handoff is genuinely warranted. Open tasks in this room:" +
			tasksList.String() +
			"\n\nOther agents in this room:" +
			agentsList.String(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The exact id of one of the open tasks listed above.",
				},
				"to_agent_name": map[string]any{
					"type":        "string",
					"description": "The exact display name of the agent to hand the task off to.",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "A summary of the work so far, for the receiving agent.",
				},
				"completed": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "What's already been done, if anything.",
				},
				"remaining": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "What's left to do, if known.",
				},
				"risks": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Anything the receiving agent should watch out for, if any.",
				},
			},
			"required": []string{"task_id", "to_agent_name", "summary"},
		},
	}
}

// executeCreateTask runs create_task's tool call to completion: a real
// task row and its TASK_CREATED event, in one transaction, published
// over hub exactly like task.Store.CreateHandler's own request/response
// path — ADR-006's "same effect as a human hitting the API directly, no
// new gate," including the same audit trail, not just the same table
// write. A malformed or empty objective is dropped silently (logged, not
// surfaced as a failed reply): the agent's own reply already generated
// successfully, and a model calling this with garbage input shouldn't
// cost the human a lost reply over it — the same tolerance
// resolveMentionToolCalls already applies to a bad mention.
func (o *Orchestrator) executeCreateTask(ctx context.Context, roomID, agentID uuid.UUID, input map[string]any) {
	objective, _ := input["objective"].(string)
	objective = strings.TrimSpace(objective)
	if objective == "" {
		log.Printf("message: agent %s called create_task with no objective, ignoring", agentID)
		return
	}

	tx, rollback, err := store.BeginTx(ctx, o.beginner)
	if err != nil {
		log.Printf("ERROR message: start transaction for agent %s create_task: %v", agentID, err)
		return
	}
	defer rollback()

	txTasks := task.NewStore(tx)
	t, err := txTasks.Create(ctx, roomID, objective, nil)
	if err != nil {
		log.Printf("ERROR message: agent %s create_task: %v", agentID, err)
		return
	}

	env := protocol.NewEnvelope(roomID, protocol.OpTaskCreate, protocol.Participant{AgentID: agentID}, map[string]any{
		"objective": t.Objective,
	})
	env.TaskID = &t.ID

	txEvents := event.NewStore(tx)
	if err := txEvents.Record(ctx, roomID, &t.ID, &agentID, task.EventTaskCreated, env.Payload); err != nil {
		log.Printf("ERROR message: record TASK_CREATED event for agent %s create_task: %v", agentID, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("ERROR message: commit agent %s create_task: %v", agentID, err)
		return
	}
	o.hub.Publish(roomID, realtime.NewEventMessage(env))
}

// executeRequestHandoff runs request_handoff's tool call up to (and only
// up to) creating the pending proposal — ADR-006 is explicit that this
// does not execute a real handoff itself. to_agent_name and task_id are
// both resolved against real room data before anything is written, the
// same non-leaking validation every mention/cascade path in this package
// already applies: a name or id the model invents just gets dropped.
func (o *Orchestrator) executeRequestHandoff(ctx context.Context, roomID, agentID uuid.UUID, input map[string]any) {
	taskIDStr, _ := input["task_id"].(string)
	taskID, err := uuid.Parse(strings.TrimSpace(taskIDStr))
	if err != nil {
		log.Printf("message: agent %s called request_handoff with an invalid task_id %q, ignoring", agentID, taskIDStr)
		return
	}
	summary, _ := input["summary"].(string)
	summary = strings.TrimSpace(summary)
	toAgentName, _ := input["to_agent_name"].(string)

	t, err := o.tasks.GetByID(ctx, taskID)
	if err != nil || t.RoomID != roomID {
		log.Printf("message: agent %s called request_handoff with task_id %s not open in this room, ignoring", agentID, taskID)
		return
	}
	if summary == "" {
		log.Printf("message: agent %s called request_handoff with no summary, ignoring", agentID)
		return
	}

	roomAgents, err := o.agents.ListByRoom(ctx, roomID)
	if err != nil {
		log.Printf("ERROR message: load room %s agents to resolve request_handoff target: %v", roomID, err)
		return
	}
	toAgentID, ok := resolveAgentName(toAgentName, roomAgents)
	if !ok {
		log.Printf("message: agent %s called request_handoff naming %q, no such agent in this room, ignoring", agentID, toAgentName)
		return
	}

	payload := map[string]any{
		"task_id":     taskID.String(),
		"to_agent_id": toAgentID.String(),
		"summary":     summary,
		"completed":   stringSlice(input["completed"]),
		"remaining":   stringSlice(input["remaining"]),
		"risks":       stringSlice(input["risks"]),
	}

	tx, rollback, err := store.BeginTx(ctx, o.beginner)
	if err != nil {
		log.Printf("ERROR message: start transaction for agent %s request_handoff: %v", agentID, err)
		return
	}
	defer rollback()

	txProposals := actionproposal.NewStore(tx)
	p, err := txProposals.Create(ctx, roomID, agentID, actionproposal.ActionRequestHandoff, payload)
	if err != nil {
		log.Printf("ERROR message: agent %s request_handoff: %v", agentID, err)
		return
	}

	env := protocol.NewEnvelope(roomID, protocol.OpActionPropose, protocol.Participant{AgentID: agentID}, map[string]any{
		"proposal_id": p.ID.String(),
		"action_type": string(p.ActionType),
		"task_id":     taskID.String(),
		"objective":   t.Objective,
		"to_agent_id": toAgentID.String(),
		"summary":     summary,
	})
	env.TaskID = &taskID

	txEvents := event.NewStore(tx)
	if err := txEvents.Record(ctx, roomID, &taskID, &agentID, actionproposal.EventActionProposed, env.Payload); err != nil {
		log.Printf("ERROR message: record ACTION_PROPOSED event for agent %s request_handoff: %v", agentID, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("ERROR message: commit agent %s request_handoff proposal: %v", agentID, err)
		return
	}
	o.hub.Publish(roomID, realtime.NewEventMessage(env))
}

// resolveAgentName matches name case-insensitively against roomAgents —
// shared by resolveMentionToolCalls and executeRequestHandoff, both of
// which resolve a model-supplied agent name against real room data
// rather than trusting a raw ID.
func resolveAgentName(name string, roomAgents []agent.Agent) (uuid.UUID, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, a := range roomAgents {
		if strings.ToLower(a.Name) == name {
			return a.ID, true
		}
	}
	return uuid.Nil, false
}

// stringSlice converts a tool call's optional string-array input field
// (already []any of untyped values, per encoding/json's own decoding of
// a JSON array into map[string]any) into []string, skipping anything
// that isn't actually a string rather than failing the whole call over
// one bad element.
func stringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
