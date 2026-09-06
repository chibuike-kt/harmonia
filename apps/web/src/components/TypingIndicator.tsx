"use client";

import { ThinkingOrb } from "thinking-orbs";

interface TypingIndicatorProps {
  agentName: string;
}

/**
 * The first genuine use of ThinkingOrb's "working" state tied to real
 * in-flight work (agents.status flipping to running on @mention
 * invocation) rather than a static presence dot — see
 * docs/design/room-mockup.html's own typing-row.
 */
export function TypingIndicator({ agentName }: TypingIndicatorProps) {
  return (
    <div className="flex items-center gap-3">
      <span className="flex h-[30px] w-[30px] shrink-0 items-center justify-center">
        <ThinkingOrb
          state="working"
          size={20}
          theme="dark"
          style={{ width: 20, height: 20 }}
        />
      </span>
      <span className="font-[family-name:var(--login-font-mono)] text-[13px] text-[var(--login-text-muted)]">
        {agentName} is thinking…
      </span>
    </div>
  );
}
