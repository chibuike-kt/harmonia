-- Widens agent_action_proposals' action_type CHECK to allow
-- propose_file_edit alongside request_handoff — the IDE design
-- overhaul's presence-gone fallback (docs/design/harmonia-ide-mockup.html):
-- an agent proposing a file edit with no human present queues a real,
-- reviewable diff instead of writing directly, reusing this exact
-- pending/approved/rejected proposal machinery rather than a second one.
ALTER TABLE agent_action_proposals DROP CONSTRAINT agent_action_proposals_type_check;
ALTER TABLE agent_action_proposals ADD CONSTRAINT agent_action_proposals_type_check
    CHECK (action_type IN ('request_handoff', 'propose_file_edit'));
