package agentloop

import "github.com/chibuike-kt/harmonia/internal/provider"

// Tool names a Session's own cycle-dispatch switch (see dispatchTool in
// loop.go) recognizes by exact string match — create_task is resolved
// through message.ExecuteCreateTask instead, reusing ADR-006 batch C's
// own tool unchanged, per the ADR's "reused, unchanged" list.
const (
	toolReadFile       = "read_file"
	toolWriteFile      = "write_file"
	toolRunCommand     = "run_command"
	toolCreateTask     = "create_task"
	toolRequestHandoff = "request_handoff"
	toolMarkDone       = "mark_done"
)

// sessionTools is the fixed tool set offered every cycle of a sustained
// loop — every existing IDE tool this Batch A build brief item 2 requires
// ("reusing every existing IDE tool unchanged") plus mark_done, the one
// genuinely new tool a bounded, self-verifying loop needs that a
// single-turn reply never did: a real, explicit way to declare the task
// finished rather than a loop with no way to stop itself short of
// exhausting a bound. create_task (message.CreateTaskTool, reused
// unchanged per the ADR) is appended by the caller, not here, since only
// the caller has this room's real open-tasks list its description needs.
func sessionTools() []provider.ToolDef {
	return []provider.ToolDef{
		{
			Name:        toolReadFile,
			Description: "Read a real file's contents from this session's open project folder, path relative to its root.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "File path, relative to the project root."},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        toolWriteFile,
			Description: "Write real content to a file in this session's open project folder, path relative to its root. Creates the file if it doesn't exist, overwrites it if it does.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "File path, relative to the project root."},
					"content": map[string]any{"type": "string", "description": "The file's complete new content."},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name: toolRunCommand,
			Description: "Run a real shell command in this session's own dedicated terminal and get back its real captured output. " +
				"This is how you actually check your own work — use it to run this project's real build, test, and lint commands, " +
				"not just to inspect files.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The real shell command to run."},
				},
				"required": []string{"command"},
			},
		},
		{
			Name: toolMarkDone,
			Description: "Declare this session's task complete and end the session. Only call this after you have actually run " +
				"this project's real build/test/lint commands via run_command in this same session and seen them pass — never " +
				"from assumption, and never just from re-reading your own changes. If nothing in this project can be built, " +
				"tested, or linted for this specific task, say so explicitly in summary and explain why real verification " +
				"doesn't apply here.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "What was done, and exactly what real verification was run and passed (or why none applies).",
					},
				},
				"required": []string{"summary"},
			},
		},
	}
}
