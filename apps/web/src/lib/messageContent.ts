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
