"use client";

import { useEffect, useRef, useState } from "react";
import type { ArtifactContent } from "./ArtifactPanel";
import { CodeBracketsIcon, FileIcon } from "./icons";
import { Tooltip } from "./Tooltip";

export interface ArtifactListItem {
  id: string;
  senderName: string;
  createdAt: string;
  kind: "code" | "text";
  language: string;
  lines: number;
  code: string;
}

interface ArtifactsMenuProps {
  artifacts: ArtifactListItem[];
  /** True when the room's message history is capped and older messages
   *  may hold artifacts this list can't see — see the room page's own
   *  comment on recencyLimit for why this is a real, not hypothetical,
   *  gap. Rendered as a plain caption, not hidden. */
  mayBeIncomplete: boolean;
  onOpenArtifact: (artifact: ArtifactContent) => void;
}

function formatRelativeTime(iso: string): string {
  const diffMinutes = Math.floor(
    (Date.now() - new Date(iso).getTime()) / 60000,
  );
  if (diffMinutes < 1) return "just now";
  if (diffMinutes < 60) return `${diffMinutes}m ago`;
  const hours = Math.floor(diffMinutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

/**
 * Room header's artifacts icon — opens a dropdown listing every file in
 * the room: every fenced code block, plus every message that's
 * essentially one large pasted block (lib/messageContent.ts's
 * collectRoomArtifacts, reusing the exact same detection MessageRow's
 * own inline chips use, just run across every loaded message instead of
 * one). Clicking a row opens it in the same side panel the inline chips
 * already open — no second display mechanism. No count badge — the icon
 * is a stable, always-present control rather than data-dependent UI.
 */
export function ArtifactsMenu({
  artifacts,
  mayBeIncomplete,
  onOpenArtifact,
}: ArtifactsMenuProps) {
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onDocumentClick(event: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
  }, []);

  // Newest first — what just got generated is what you're most likely
  // looking for, same reasoning as any other "recent items" list.
  const sorted = [...artifacts].sort((a, b) =>
    a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : 0,
  );

  return (
    <div ref={menuRef} className="relative">
      <Tooltip label={open ? "Hide artifacts" : "Artifacts in this room"}>
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className={`flex rounded-md p-1.5 ${
            open
              ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
              : "text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
          }`}
        >
          <FileIcon />
        </button>
      </Tooltip>

      {open && (
        <div className="absolute right-0 top-full z-10 mt-1.5 w-[300px] rounded-[10px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
          <div className="border-b border-[var(--login-border)] px-3 py-2.5 text-[12.5px] font-medium text-[var(--login-text)]">
            Artifacts in this room
          </div>

          <div className="no-scrollbar max-h-[320px] overflow-y-auto p-1.5">
            {sorted.length === 0 ? (
              <p className="px-2 py-3 text-[12.5px] text-[var(--login-text-muted)]">
                No artifacts yet.
              </p>
            ) : (
              sorted.map((a) => (
                <button
                  key={a.id}
                  type="button"
                  onClick={() => {
                    onOpenArtifact({
                      label:
                        a.kind === "text"
                          ? `${a.senderName.toLowerCase()}-pasted-text.txt`
                          : `${a.senderName.toLowerCase()}-snippet.${a.language}`,
                      language: a.language,
                      code: a.code,
                      kind: a.kind,
                    });
                    setOpen(false);
                  }}
                  className="flex w-full flex-col items-start gap-0.5 rounded-lg px-2.5 py-2 text-left hover:bg-[var(--login-surface-2)]"
                >
                  <span className="flex w-full items-center gap-1.5 font-[family-name:var(--login-font-mono)] text-[12px] text-[var(--login-text)]">
                    {a.kind === "text" ? <FileIcon /> : <CodeBracketsIcon />}
                    {a.kind === "text" ? "Pasted text" : a.language} · {a.lines}{" "}
                    line{a.lines === 1 ? "" : "s"}
                  </span>
                  <span className="text-[11.5px] text-[var(--login-text-muted)]">
                    {a.senderName} · {formatRelativeTime(a.createdAt)}
                  </span>
                </button>
              ))
            )}
          </div>

          {mayBeIncomplete && (
            <div className="border-t border-[var(--login-border)] px-3 py-2 text-[11px] leading-[1.5] text-[var(--login-text-muted)]">
              Reflects messages currently loaded in this room, not necessarily
              its full history.
            </div>
          )}
        </div>
      )}
    </div>
  );
}
