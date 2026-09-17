ALTER TABLE agent_action_proposals DROP CONSTRAINT agent_action_proposals_type_check;
ALTER TABLE agent_action_proposals ADD CONSTRAINT agent_action_proposals_type_check
    CHECK (action_type IN ('request_handoff'));
