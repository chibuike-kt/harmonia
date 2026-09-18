"use client";

import { useState } from "react";
import { Tooltip } from "@/components/Tooltip";
import { FileGlyph } from "./ExplorerPanel";

export interface GitFileStatus {
  path: string;
  staged: string;
  worktree: string;
}

export interface GitStatusState {
  repo: boolean;
  branch: string;
  files: GitFileStatus[];
}

// Real, plain-language labels for git's own real one-letter porcelain
// status codes — "M"/"A"/"D"/"R"/"?" etc. straight from a real `git
// status --porcelain`, not a simplified "changed" bucket.
const STATUS_LABEL: Record<string, string> = {
  M: "Modified",
  A: "Added",
  D: "Deleted",
  R: "Renamed",
  C: "Copied",
  U: "Conflict",
  "?": "Untracked",
};

// Real VS Code-convention colors for git's own real one-letter porcelain
// codes — a single colored letter badge, not a full word, is the actual
// well-known convention; the full word still surfaces as this badge's
// own tooltip rather than disappearing.
const STATUS_COLOR: Record<string, string> = {
  M: "text-[var(--room-warn)]",
  A: "text-emerald-400",
  D: "text-red-400",
  R: "text-sky-400",
  C: "text-sky-400",
  U: "text-red-500",
  "?": "text-emerald-300",
};

function statusLabel(code: string): string {
  return STATUS_LABEL[code] ?? code;
}

function statusColor(code: string): string {
  return STATUS_COLOR[code] ?? "text-[var(--ide-text-muted)]";
}

function FileRow({
  file,
  staged,
  onToggle,
}: {
  file: GitFileStatus;
  staged: boolean;
  onToggle: () => void;
}) {
  const code = staged ? file.staged : file.worktree;
  const name = file.path.split("/").pop() ?? file.path;
  const dir = file.path.slice(0, file.path.length - name.length - 1);
  return (
    <div className="group flex items-center gap-2 rounded-md px-2 py-1 text-[12.5px] hover:bg-[var(--ide-surface)]">
      <FileGlyph isDir={false} name={name} />
      <span className="flex min-w-0 items-baseline gap-1.5 truncate">
        <span className="truncate text-[var(--ide-text)]">{name}</span>
        {dir && (
          <span className="truncate text-[11px] text-[var(--ide-text-muted)]">
            {dir}
          </span>
        )}
      </span>
      <Tooltip label={statusLabel(code)} side="top" align="end">
        <span
          className={`ml-auto shrink-0 font-[family-name:var(--login-font-mono)] text-[11px] font-semibold ${statusColor(code)}`}
        >
          {code.trim() || "?"}
        </span>
      </Tooltip>
      <Tooltip label={staged ? "Unstage" : "Stage"} side="top" align="end">
        <button
          type="button"
          onClick={onToggle}
          className="flex h-5 w-5 shrink-0 items-center justify-center rounded text-[var(--ide-text-muted)] opacity-0 hover:bg-[var(--login-accent)] hover:text-[#0a0c11] group-hover:opacity-100"
        >
          {staged ? "−" : "+"}
        </button>
      </Tooltip>
    </div>
  );
}

/**
 * The IDE's own real Source Control view — real `git status`, real
 * stage/unstage (per file), and a real commit against whatever is
 * genuinely staged, all through internal/companion's own real git_*
 * capabilities. Scoped deliberately to status + stage + commit for this
 * pass — no diff view, no push/pull/branch switching yet; those are
 * real, separate scope, not part of this first version.
 */
export function SourceControlPanel({
  status,
  onStage,
  onUnstage,
  onCommit,
  onRefresh,
}: {
  status: GitStatusState | null;
  onStage: (path: string) => void;
  onUnstage: (path: string) => void;
  onCommit: (message: string) => void;
  onRefresh: () => void;
}) {
  const [message, setMessage] = useState("");

  if (!status) {
    return (
      <div className="flex h-full items-center justify-center px-4 text-center text-[12.5px] text-[var(--ide-text-muted)]">
        Loading git status…
      </div>
    );
  }
  if (!status.repo) {
    return (
      <div className="flex h-full items-center justify-center px-4 text-center text-[12.5px] text-[var(--ide-text-muted)]">
        This folder isn&apos;t a real git repository.
      </div>
    );
  }

  // A file can genuinely appear in both lists at once — staged, with
  // further real changes made since (git's own real "MM" case) — so
  // these two filters are independent, not a mutually-exclusive split.
  const staged = status.files.filter(
    (f) => f.staged !== "" && f.staged !== " " && f.staged !== "?",
  );
  const unstaged = status.files.filter(
    (f) => f.worktree !== "" && f.worktree !== " ",
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center justify-between px-3 pt-3 pb-2">
        <span className="truncate font-[family-name:var(--login-font-mono)] text-[10.5px] tracking-wide text-[var(--ide-text-muted)] uppercase">
          {status.branch || "detached HEAD"}
        </span>
        <Tooltip label="Refresh" side="top" align="end">
          <button
            type="button"
            onClick={onRefresh}
            className="flex h-5 w-5 items-center justify-center rounded text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface)] hover:text-[var(--ide-text)]"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <path d="M13 8a5 5 0 11-1.6-3.65M13 3v3.5H9.5" />
            </svg>
          </button>
        </Tooltip>
      </div>

      <div className="flex shrink-0 flex-col gap-1.5 px-2 pb-2">
        <textarea
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Commit message"
          rows={2}
          className="resize-none rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2.5 py-1.5 text-[12.5px] text-[var(--ide-text)] outline-none placeholder:text-[var(--ide-text-muted)] focus:border-[var(--login-accent)]"
        />
        <button
          type="button"
          disabled={!message.trim() || staged.length === 0}
          onClick={() => {
            onCommit(message.trim());
            setMessage("");
          }}
          className="rounded-md bg-[var(--login-accent)] px-3 py-1.5 text-[12.5px] font-medium text-[var(--ide-bg)] hover:bg-[#63e0d1] disabled:cursor-not-allowed disabled:opacity-40"
        >
          Commit {staged.length > 0 ? `(${staged.length})` : ""}
        </button>
      </div>

      <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto px-2">
        {staged.length > 0 && (
          <div className="mb-2">
            <div className="px-2 py-1 text-[11px] font-medium text-[var(--ide-text-muted)] uppercase">
              Staged Changes
            </div>
            {staged.map((f) => (
              <FileRow
                key={f.path}
                file={f}
                staged
                onToggle={() => onUnstage(f.path)}
              />
            ))}
          </div>
        )}
        <div>
          <div className="px-2 py-1 text-[11px] font-medium text-[var(--ide-text-muted)] uppercase">
            Changes
          </div>
          {unstaged.length === 0 && (
            <p className="px-2 py-2 text-[12px] text-[var(--ide-text-muted)]">
              No real changes.
            </p>
          )}
          {unstaged.map((f) => (
            <FileRow
              key={f.path}
              file={f}
              staged={false}
              onToggle={() => onStage(f.path)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
