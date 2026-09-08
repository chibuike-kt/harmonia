"use client";

import { useState } from "react";
import { PASTED_TEXT_TAG, parseMessageContent } from "@/lib/messageContent";
import type { ArtifactContent } from "./ArtifactPanel";
import {
  CheckIcon,
  CodeBracketsIcon,
  CopyIcon,
  FileIcon,
  PinIcon,
  ReplyArrowIcon,
  RetryIcon,
  WarnIcon,
} from "./icons";
import { AgentAvatarGlyph } from "./providerLogos";
import { Tooltip } from "./Tooltip";

// How long the copy button shows its checkmark before reverting — long
// enough to register as feedback, short enough not to look stuck.
const COPY_FEEDBACK_MS = 1500;

export interface ChatMessage {
  id: string;
  room_id: string;
  sender_kind: "human" | "agent";
  user_id?: string;
  agent_id?: string;
  mentioned_agent_ids?: string[];
  reply_to_message_id?: string;
  content: string;
  created_at: string;
  input_tokens?: number;
  output_tokens?: number;
  /** Client-side only, never from the wire — see room page's own comment. */
  failed?: boolean;
}

interface MessageRowProps {
  message: ChatMessage;
  senderName: string;
  /** Undefined for a human sender — AgentAvatarGlyph falls back to
   *  initials whenever there's no provider to look up a logo for. */
  senderProvider?: string;
  /** The @mentioned agents' display names, resolved from
   *  message.mentioned_agent_ids (ADR-006 batch A: a message can
   *  address more than one) — set only on a human message that
   *  actually addressed at least one. Rendered as visible tags above
   *  the bubble: without them, a mention was invisible in the
   *  timeline — the message just looked like plain text and the
   *  agent(s) replied with no shown connection to it. */
  mentionedAgentNames?: string[];
  /** Set only when reply_to_message_id points somewhere still resolvable
   *  and it isn't the immediately preceding timeline item — see
   *  room page's own "replying to" placement logic. */
  replyPreview?: { senderName: string; snippet: string };
  onOpenArtifact: (artifact: ArtifactContent) => void;
  /** Already pinned as a decision — reflected as a filled, inert pin
   *  rather than a clickable button, since pinning has no "undo" this
   *  phase builds (see the build brief: manual pin only, no unpin). */
  pinned: boolean;
  onPinDecision: () => void;
  /** Undefined for a human message — there's nothing to regenerate for
   *  one of the human's own messages, only for an agent's reply. */
  onRetry?: () => void;
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
}

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // No clipboard access — nothing more useful to do than leave it be.
    return false;
  }
}

// Copy button shared by both the human bubble and the agent row: briefly
// swaps to a checkmark after a successful copy, then reverts on its own —
// the state is local to this button, never lifted, since it's purely
// transient per-click feedback with nothing else in the room depending
// on it.
function CopyButton({ content }: { content: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Tooltip label={copied ? "Copied!" : "Copy"}>
      <button
        type="button"
        onClick={() =>
          void copyText(content).then((ok) => {
            if (!ok) return;
            setCopied(true);
            setTimeout(() => setCopied(false), COPY_FEEDBACK_MS);
          })
        }
        className="flex h-[26px] w-[26px] items-center justify-center rounded-md text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </button>
    </Tooltip>
  );
}

// Same visual language as the code-artifact chip below (icon + label in
// a bordered box), just FileIcon instead of CodeBracketsIcon — this is
// generic pasted text, not a code snippet, and the composer's own
// pending-paste chip (Composer.tsx) uses this exact same look so the
// two read as one consistent feature, not two unrelated ones.
function PastedTextChip({
  lines,
  onClick,
}: {
  lines: number;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="mt-1.5 flex items-center gap-2 rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3 py-2 font-[family-name:var(--login-font-mono)] text-[12.5px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
    >
      <FileIcon />
      Pasted text · {lines} line{lines === 1 ? "" : "s"}
    </button>
  );
}

// Shared by both the human bubble and the agent row: a message's
// content, segment by segment — plain paragraphs for text, a code chip
// for a real fenced code block, a pasted-text chip for a
// PASTED_TEXT_TAG block (the composer's paste-as-file marker — see
// Composer.tsx's handleSend). Rendering this the same way regardless of
// sender is what keeps typed text and a pasted block visually separate
// in a mixed message: each is its own segment, not one blob decided by
// the whole message's size.
function renderContent(
  content: string,
  senderName: string,
  onOpenArtifact: (artifact: ArtifactContent) => void,
) {
  return parseMessageContent(content).map((seg, i) => {
    if (seg.type === "text") {
      return (
        seg.text.trim() && (
          <p key={i} className="mb-2 whitespace-pre-wrap last:mb-0">
            {seg.text.trim()}
          </p>
        )
      );
    }
    if (seg.language === PASTED_TEXT_TAG) {
      return (
        <PastedTextChip
          key={i}
          lines={seg.lines}
          onClick={() =>
            onOpenArtifact({
              label: `${senderName.toLowerCase()}-pasted-text.txt`,
              language: "text",
              code: seg.code,
              kind: "text",
            })
          }
        />
      );
    }
    return (
      <button
        key={i}
        type="button"
        onClick={() =>
          onOpenArtifact({
            label: `${senderName.toLowerCase()}-snippet.${seg.language}`,
            language: seg.language,
            code: seg.code,
            kind: "code",
          })
        }
        className="mt-1.5 flex items-center gap-2 rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3 py-2 font-[family-name:var(--login-font-mono)] text-[12.5px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
      >
        <CodeBracketsIcon />
        {seg.language} · {seg.lines} line{seg.lines === 1 ? "" : "s"}
      </button>
    );
  });
}

/**
 * One row of the room timeline's chat grammar — a Slack-style row for an
 * agent (avatar + name + timestamp, left-aligned, no bubble) or a
 * right-aligned bubble for the human, matching
 * docs/design/room-mockup.html exactly. A failure message (see the room
 * page's own client-side `failed` flag) gets a visibly distinct, non-
 * alarming treatment — a warning-tinted left border and icon, not a
 * red error banner: it's still a real, readable agent-style message,
 * just flagged as something that didn't go as intended.
 */
export function MessageRow({
  message,
  senderName,
  senderProvider,
  mentionedAgentNames,
  replyPreview,
  onOpenArtifact,
  pinned,
  onPinDecision,
  onRetry,
}: MessageRowProps) {
  const isHuman = message.sender_kind === "human";

  if (isHuman) {
    return (
      <div className="group/msg flex justify-end">
        <div className="max-w-[78%]">
          {mentionedAgentNames && mentionedAgentNames.length > 0 && (
            <div className="mb-1 flex items-center justify-end gap-1 font-[family-name:var(--login-font-mono)] text-[12px] text-[var(--login-text-secondary)]">
              <ReplyArrowIcon />
              {mentionedAgentNames.map((name) => `@${name}`).join(" ")}
            </div>
          )}
          <div className="rounded-[14px_14px_3px_14px] border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3.5 py-2.5 text-[16px] leading-[1.6] text-[var(--login-text)]">
            {renderContent(message.content, senderName, onOpenArtifact)}
          </div>
          <div className="mt-1 flex items-center justify-end gap-2">
            <span className="font-[family-name:var(--login-font-mono)] text-[11.5px] text-[var(--login-text-muted)]">
              {formatTime(message.created_at)}
            </span>
            <div className="flex gap-0.5 opacity-0 transition-opacity group-hover/msg:opacity-100">
              <CopyButton content={message.content} />
              <Tooltip
                label={pinned ? "Pinned as decision" : "Pin as decision"}
              >
                <button
                  type="button"
                  disabled={pinned}
                  onClick={onPinDecision}
                  className="flex h-[26px] w-[26px] items-center justify-center rounded-md text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)] disabled:cursor-default disabled:text-[var(--login-accent)] disabled:hover:bg-transparent"
                >
                  <PinIcon filled={pinned} />
                </button>
              </Tooltip>
            </div>
          </div>
        </div>
      </div>
    );
  }

  const isFailure = !!message.failed;

  return (
    <div
      className={`group/msg flex gap-3 ${isFailure ? "border-l-2 border-[var(--room-warn)] pl-3" : ""}`}
    >
      <div className="flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-[11px] font-semibold">
        <AgentAvatarGlyph
          provider={senderProvider}
          name={senderName}
          size={13}
        />
      </div>
      <div className="min-w-0 flex-1">
        {replyPreview && (
          <div className="mb-1 flex items-center gap-1.5 font-[family-name:var(--login-font-mono)] text-[12px] text-[var(--login-text-secondary)]">
            <ReplyArrowIcon />
            <span>
              Replying to{" "}
              <b className="font-[family-name:var(--login-font-sans)] font-semibold text-[var(--login-text)]">
                {replyPreview.senderName}
              </b>
              : &quot;{replyPreview.snippet}&quot;
            </span>
          </div>
        )}
        <div className="mb-0.5 flex items-baseline gap-2">
          <span className="text-[15px] font-semibold text-[var(--login-text)]">
            {senderName}
          </span>
          <span className="font-[family-name:var(--login-font-mono)] text-[11.5px] text-[var(--login-text-muted)]">
            {formatTime(message.created_at)}
          </span>
          {isFailure && (
            <span className="flex items-center gap-1 text-[11px] text-[var(--room-warn)]">
              <WarnIcon />
              couldn&apos;t reply
            </span>
          )}
        </div>
        <div className="text-[16px] leading-[1.6] text-[var(--login-text)]">
          {renderContent(message.content, senderName, onOpenArtifact)}
        </div>
        <div className="mt-1.5 flex gap-0.5 opacity-0 transition-opacity group-hover/msg:opacity-100">
          <CopyButton content={message.content} />
          {onRetry && (
            <Tooltip label="Retry">
              <button
                type="button"
                onClick={onRetry}
                className="flex h-[26px] w-[26px] items-center justify-center rounded-md text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
              >
                <RetryIcon />
              </button>
            </Tooltip>
          )}
          <Tooltip label={pinned ? "Pinned as decision" : "Pin as decision"}>
            <button
              type="button"
              disabled={pinned}
              onClick={onPinDecision}
              className="flex h-[26px] w-[26px] items-center justify-center rounded-md text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)] disabled:cursor-default disabled:text-[var(--login-accent)] disabled:hover:bg-transparent"
            >
              <PinIcon filled={pinned} />
            </button>
          </Tooltip>
        </div>
      </div>
    </div>
  );
}

// Shared sender-name resolution so the room page and this file can't
// drift on how a message's display name is derived.
export function displaySenderName(
  message: Pick<ChatMessage, "sender_kind" | "agent_id">,
  agentNames: Record<string, string>,
  humanName: string,
): string {
  if (message.sender_kind === "human") return humanName;
  return (message.agent_id && agentNames[message.agent_id]) || "Agent";
}
