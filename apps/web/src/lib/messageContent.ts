// Splits a message's raw content into plain-text and fenced-code-block
// segments — the lightweight version of the product doc's Artifact
// System (section 60) the build brief calls for: no new storage, no
// versioning. A code block is derived by re-parsing the same message
// content every time, not persisted separately, so reopening the panel
// later is just re-parsing the same durable message row.

export interface TextSegment {
  type: "text";
  text: string;
}

export interface CodeSegment {
  type: "code";
  language: string;
  code: string;
  lines: number;
}

export type ContentSegment = TextSegment | CodeSegment;

const FENCE = /```([\w-]*)\n([\s\S]*?)```/g;

// The "language" tag the composer wraps a pasted-as-file block in (see
// Composer.tsx's handleSend) — reuses the exact same fence syntax a real
// code block uses, just with this reserved tag instead of a language
// name, so a message that's part typed text and part pasted block(s)
// parses into separate segments instead of one indivisible blob. This is
// what actually fixes typed text getting swallowed into the pasted
// block: before this, a whole-message heuristic decided "is this
// message essentially one big paste," and anything you typed alongside
// a paste counted toward that same blob. Marking the boundary in the
// content itself removes the need to guess at all.
export const PASTED_TEXT_TAG = "pasted-text";

/**
 * Splits content on ```lang\n...\n``` fences. Only real fenced blocks
 * count as "substantial standalone content" worth a chip — inline
 * `code` spans stay inline text, matching how the mockup itself only
 * ever chips a full fenced block, never a short inline snippet.
 */
export function parseMessageContent(content: string): ContentSegment[] {
  const segments: ContentSegment[] = [];
  let lastIndex = 0;

  for (const match of content.matchAll(FENCE)) {
    const [full, language, code] = match;
    const index = match.index ?? 0;
    if (index > lastIndex) {
      segments.push({ type: "text", text: content.slice(lastIndex, index) });
    }
    const trimmed = code.replace(/\n$/, "");
    segments.push({
      type: "code",
      language: language || "text",
      code: trimmed,
      lines: trimmed.length === 0 ? 0 : trimmed.split("\n").length,
    });
    lastIndex = index + full.length;
  }

  if (lastIndex < content.length) {
    segments.push({ type: "text", text: content.slice(lastIndex) });
  }
  if (segments.length === 0) {
    segments.push({ type: "text", text: content });
  }
  return segments;
}

export interface RoomArtifact {
  /** message id + segment index — stable within one parse of one
   *  message, which is all a room-wide artifact list ever needs: it's
   *  rebuilt from the same loaded messages every render, never persisted. */
  id: string;
  messageId: string;
  /** "text" is a PASTED_TEXT_TAG segment — a pasted-as-file block, not
   *  real code. */
  kind: "code" | "text";
  language: string;
  code: string;
  lines: number;
}

/**
 * Runs parseMessageContent across every message given, in order, and
 * collects every file-worthy artifact room-wide — every fenced code
 * block, plus every pasted-as-file block (a PASTED_TEXT_TAG segment,
 * wherever it falls in the message, alongside typed text or other
 * blocks). This is "every file in the room," not just code — a pasted
 * text block is a file too. Callers own the "which messages" question
 * entirely: this has no opinion on load windows, ordering beyond input
 * order, or how many messages that is. See the room page's own comment
 * where this is called for the real caveat that matters here — the
 * input is whatever's currently loaded, not necessarily the room's full
 * history.
 */
export function collectRoomArtifacts(
  messages: { id: string; content: string }[],
): RoomArtifact[] {
  const artifacts: RoomArtifact[] = [];
  for (const m of messages) {
    parseMessageContent(m.content).forEach((seg, i) => {
      if (seg.type !== "code") return;
      artifacts.push({
        id: `${m.id}-${i}`,
        messageId: m.id,
        kind: seg.language === PASTED_TEXT_TAG ? "text" : "code",
        language: seg.language,
        code: seg.code,
        lines: seg.lines,
      });
    });
  }
  return artifacts;
}

// Paste-as-file threshold — matches Claude's own composer behavior in
// spirit, not in verified exact numbers: a tunable starting point, not a
// claim that this is the real threshold some specific product ships.
// Bumped up from an initial 1000/15 after live testing felt too eager
// to intercept ordinary-sized pasted text.
// Shared by the composer (intercepting a paste this large) — the
// timeline no longer needs this threshold itself: whether a block
// renders as a chip is decided once, at paste time, and carried by the
// PASTED_TEXT_TAG marker from then on, not re-guessed from raw size on
// every render.
export const PASTE_CHAR_THRESHOLD = 3000;
export const PASTE_LINE_THRESHOLD = 25;

export function exceedsPasteThreshold(text: string): boolean {
  return (
    text.length > PASTE_CHAR_THRESHOLD ||
    text.split("\n").length > PASTE_LINE_THRESHOLD
  );
}
