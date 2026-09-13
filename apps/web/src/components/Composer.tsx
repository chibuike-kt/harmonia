"use client";

import { useEffect, useRef, useState } from "react";
import { exceedsPasteThreshold, PASTED_TEXT_TAG } from "@/lib/messageContent";
import { FileCard } from "./FileCard";
import { FileIcon, PlusIcon, SearchIcon, SendIcon } from "./icons";
import { AgentAvatarGlyph } from "./providerLogos";

interface PastedAttachment {
  id: string;
  text: string;
  lines: number;
}

// A real attached file (ADR-008 batch A) — capped at 1 MB client-side
// too, mirroring internal/message/http.go's own maxAttachmentBytes
// exactly: failing fast here is a better experience than waiting on a
// round trip just to get the same 400 back, but the backend's own check
// is the real guard, not this one (a client can always be bypassed).
export interface PendingFileAttachment {
  filename: string;
  mimeType: string;
  /** Base64, no `data:...;base64,` prefix — exactly what
   *  internal/message.attachmentRequest.Content expects; encoding/json
   *  decodes a []byte field from base64 automatically. */
  contentBase64: string;
  sizeBytes: number;
}

const MAX_ATTACHMENT_BYTES = 1024 * 1024;

export interface RoomAgent {
  id: string;
  name: string;
  provider: string;
}

interface ComposerProps {
  agents: RoomAgent[];
  disabled?: boolean;
  onSend: (
    content: string,
    mentionedAgentIds: string[],
    attachment?: PendingFileAttachment,
    forceSearch?: boolean,
  ) => void;
}

const MAX_HEIGHT = 160;

// FileReader.readAsDataURL gives "data:<mime>;base64,<data>" — this is
// the browser's own optimized base64 encoder, deliberately not a manual
// byte-to-base64 loop: spreading a large Uint8Array into btoa/
// String.fromCharCode can throw "Maximum call stack size exceeded" well
// under this composer's own 1 MB cap on some engines, a real footgun
// for exactly the file sizes this feature targets.
function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = reader.result as string;
      const comma = result.indexOf(",");
      resolve(comma >= 0 ? result.slice(comma + 1) : result);
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
}

/**
 * Message composer — text input, send, an @-picker for the room's own
 * agents that allows selecting more than one (sends the structured
 * mentioned_agent_ids array the backend expects, never text-parsed —
 * ADR-006 batch A), and a "+" menu offering a real file attachment
 * (ADR-008 batch A) alongside a real per-message "Search the web" force
 * attach (ADR-008 batch B) — forces this one message's reply through a
 * real web search via RequireToolCall, independent of whatever the
 * room's own web_search toggle is set to (see RoomInfoPanel).
 */
export function Composer({ agents, disabled, onSend }: ComposerProps) {
  const [value, setValue] = useState("");
  const [plusOpen, setPlusOpen] = useState(false);
  const [mentionedAgents, setMentionedAgents] = useState<RoomAgent[]>([]);
  const [pastedAttachments, setPastedAttachments] = useState<
    PastedAttachment[]
  >([]);
  const [fileAttachment, setFileAttachment] =
    useState<PendingFileAttachment | null>(null);
  const [attachmentError, setAttachmentError] = useState<string | null>(null);
  const [forceSearch, setForceSearch] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const el = textareaRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, MAX_HEIGHT) + "px";
  }, [value]);

  useEffect(() => {
    function onDocumentClick(event: MouseEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setPlusOpen(false);
      }
    }
    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
  }, []);

  const handleFileSelected = async (file: File) => {
    setAttachmentError(null);
    if (file.size > MAX_ATTACHMENT_BYTES) {
      setAttachmentError(
        `${file.name} is too large — attachments are capped at 1 MB.`,
      );
      return;
    }
    try {
      const contentBase64 = await readFileAsBase64(file);
      setFileAttachment({
        filename: file.name,
        // A dragged/selected file with no recognized type (file.type ===
        // "") isn't rare — apply a generic fallback rather than sending
        // an empty mime_type the backend would reject as a missing field.
        mimeType: file.type || "application/octet-stream",
        contentBase64,
        sizeBytes: file.size,
      });
    } catch {
      setAttachmentError(`Couldn't read ${file.name}.`);
    }
  };

  const handleSend = () => {
    const trimmed = value.trim();
    if (disabled) return;
    if (!trimmed && pastedAttachments.length === 0 && !fileAttachment) return;
    // No schema change: the full pasted text still goes out as part of
    // the message's ordinary content field. Each pasted block is wrapped
    // in a ```pasted-text fence — the same fence syntax a real code
    // block uses, just with this reserved tag — so parseMessageContent
    // splits it into its own segment instead of merging it into
    // whatever else is in the message. That's what keeps typed text from
    // getting swallowed into the pasted-file chip: without a marked
    // boundary, a message that's part typed and part pasted has no way
    // to tell the two apart once they're joined into one string. A chip
    // lives outside the textarea entirely (same as the mentioned-agent
    // chip above it), so there's no tracked cursor position to splice
    // pasted content back into — it's appended after typed text, not
    // spliced into the middle. A judgment call, not a precision guarantee.
    const pastedBlocks = pastedAttachments.map(
      (p) => "```" + PASTED_TEXT_TAG + "\n" + p.text + "\n```",
    );
    const parts = [trimmed, ...pastedBlocks].filter((p) => p !== "");
    onSend(
      parts.join("\n\n"),
      mentionedAgents.map((a) => a.id),
      fileAttachment ?? undefined,
      forceSearch || undefined,
    );
    setValue("");
    setMentionedAgents([]);
    setPastedAttachments([]);
    setFileAttachment(null);
    setAttachmentError(null);
    setForceSearch(false);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const text = e.clipboardData.getData("text/plain");
    if (!text || !exceedsPasteThreshold(text)) return;
    // Below the threshold, do nothing — let the browser's default paste
    // behavior insert it into the textarea like any normal paste.
    e.preventDefault();
    setPastedAttachments((prev) => [
      ...prev,
      { id: crypto.randomUUID(), text, lines: text.split("\n").length },
    ]);
  };

  return (
    <div className="flex justify-center px-6 pb-5 pt-3.5">
      <div
        ref={containerRef}
        onDragOver={(e) => {
          // Only text/file drags need preventDefault to become droppable
          // here — this doesn't affect ordinary text selection/drag
          // inside the textarea itself.
          if (e.dataTransfer.types.includes("Files")) e.preventDefault();
        }}
        onDrop={(e) => {
          if (e.dataTransfer.files.length === 0) return;
          e.preventDefault();
          void handleFileSelected(e.dataTransfer.files[0]);
        }}
        className="relative w-full max-w-[720px] rounded-[14px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-2.5 pl-3.5"
      >
        <input
          ref={fileInputRef}
          type="file"
          className="hidden"
          onChange={(e) => {
            const file = e.currentTarget.files?.[0];
            e.currentTarget.value = ""; // lets picking the same file twice re-fire onChange
            if (file) void handleFileSelected(file);
          }}
        />

        {plusOpen && (
          <div className="absolute bottom-[calc(100%+8px)] left-2.5 flex min-w-[200px] flex-col gap-px rounded-[10px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-1.5 shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
            <button
              type="button"
              onClick={() => {
                setPlusOpen(false);
                setForceSearch((v) => !v);
              }}
              className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
            >
              <SearchIcon />
              Search the web
              {forceSearch && (
                <span className="ml-auto font-[family-name:var(--login-font-mono)] text-[10.5px] text-[var(--login-accent)]">
                  on
                </span>
              )}
            </button>
            <button
              type="button"
              onClick={() => {
                setPlusOpen(false);
                fileInputRef.current?.click();
              }}
              className="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
            >
              <FileIcon />
              Add a file
            </button>
          </div>
        )}

        {mentionedAgents.length > 0 && (
          <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-1">
            {mentionedAgents.map((a) => (
              <span
                key={a.id}
                className="flex items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] py-0.5 pl-1 pr-2 text-[12px] text-[var(--login-text-secondary)]"
              >
                <span className="flex h-4 w-4 items-center justify-center rounded-full bg-[var(--login-border-strong)] text-[8px] font-semibold text-[var(--login-accent)]">
                  <AgentAvatarGlyph
                    provider={a.provider}
                    name={a.name}
                    size={9}
                  />
                </span>
                @{a.name}
                <button
                  type="button"
                  aria-label={`Remove @${a.name}`}
                  onClick={() =>
                    setMentionedAgents((prev) =>
                      prev.filter((x) => x.id !== a.id),
                    )
                  }
                  className="ml-0.5 text-[var(--login-text-muted)] hover:text-[var(--login-text)]"
                >
                  ×
                </button>
              </span>
            ))}
          </div>
        )}

        {agents.some((a) => !mentionedAgents.some((m) => m.id === a.id)) && (
          <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-1">
            <span className="font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-muted)]">
              Address:
            </span>
            {agents
              .filter((a) => !mentionedAgents.some((m) => m.id === a.id))
              .map((a) => (
                <button
                  key={a.id}
                  type="button"
                  onClick={() => setMentionedAgents((prev) => [...prev, a])}
                  className="flex items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] py-1 pl-1.5 pr-2.5 text-[12px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
                >
                  <span className="flex h-4 w-4 items-center justify-center rounded-full bg-[var(--login-border-strong)] text-[8px] font-semibold text-[var(--login-accent)]">
                    <AgentAvatarGlyph
                      provider={a.provider}
                      name={a.name}
                      size={9}
                    />
                  </span>
                  @{a.name}
                </button>
              ))}
          </div>
        )}

        {pastedAttachments.length > 0 && (
          <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-1">
            {pastedAttachments.map((p, i) => (
              <FileCard
                key={p.id}
                name={
                  pastedAttachments.length === 1
                    ? "pasted-text.txt"
                    : `pasted-text-${i + 1}.txt`
                }
                subtitle={`Pasted text · ${p.lines} line${p.lines === 1 ? "" : "s"}`}
                kind="text"
                onRemove={() =>
                  setPastedAttachments((prev) =>
                    prev.filter((x) => x.id !== p.id),
                  )
                }
              />
            ))}
          </div>
        )}

        {fileAttachment && (
          <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-1">
            <FileCard
              name={fileAttachment.filename}
              subtitle={`${fileAttachment.mimeType} · ${formatFileSize(fileAttachment.sizeBytes)}`}
              kind="file"
              onRemove={() => setFileAttachment(null)}
            />
          </div>
        )}
        {forceSearch && (
          <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-1">
            <span className="flex items-center gap-1.5 rounded-full border border-[var(--login-accent)] bg-[var(--login-accent)]/10 py-1 pl-2 pr-2.5 text-[12px] text-[var(--login-accent)]">
              <SearchIcon />
              Search the web
              <button
                type="button"
                aria-label="Cancel search the web"
                onClick={() => setForceSearch(false)}
                className="ml-0.5 text-[var(--login-accent)] hover:opacity-70"
              >
                ×
              </button>
            </span>
          </div>
        )}
        {attachmentError && (
          <p className="mb-1.5 px-1 text-[12px] text-[var(--room-warn)]">
            {attachmentError}
          </p>
        )}

        <div className="flex items-end gap-2.5">
          <button
            type="button"
            aria-label="More options"
            onClick={() => setPlusOpen((o) => !o)}
            className="flex h-[34px] w-[34px] shrink-0 items-center justify-center rounded-lg text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text-secondary)]"
          >
            <PlusIcon size={17} strokeWidth={1.6} />
          </button>
          <textarea
            ref={textareaRef}
            rows={1}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={handleKeyDown}
            onPaste={handlePaste}
            placeholder="Message this room — @mention an agent to address it"
            className="no-scrollbar max-h-[160px] flex-1 resize-none bg-transparent py-2 font-[family-name:var(--login-font-sans)] text-[16px] leading-[1.5] text-[var(--login-text)] outline-none placeholder:text-[var(--login-text-muted)]"
          />
          <button
            type="button"
            aria-label="Send message"
            disabled={
              disabled ||
              (value.trim() === "" &&
                pastedAttachments.length === 0 &&
                !fileAttachment)
            }
            onClick={handleSend}
            className="flex h-[34px] w-[34px] shrink-0 items-center justify-center rounded-lg bg-[var(--login-accent)] text-[var(--login-bg)] hover:bg-[#63e0d1] disabled:cursor-not-allowed disabled:opacity-40"
          >
            <SendIcon />
          </button>
        </div>
      </div>
    </div>
  );
}
