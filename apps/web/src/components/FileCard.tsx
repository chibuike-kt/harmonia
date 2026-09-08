"use client";

import { CloseIcon, CodeBracketsIcon, FileIcon } from "./icons";

export interface FileCardProps {
  /** The file's actual name — lib/messageContent's suggestedName, real
   *  or a heuristic fallback, never the old provider-based label. */
  name: string;
  /** "go · 71 lines" / "Pasted text · 30 lines". */
  subtitle: string;
  kind: "code" | "text";
  /** Set to make the whole card an "open" affordance — the inline chip
   *  and the room's artifacts menu both open a viewer this way. */
  onClick?: () => void;
  /** Set to add a remove action instead — the composer's own pending
   *  attachment, before it's ever sent, has something to remove rather
   *  than open. */
  onRemove?: () => void;
}

/**
 * The file-card treatment this app's artifact surfaces share: an icon
 * box, the file's real name as the title, a type/line-count subtitle,
 * and one clear action — matching Claude.ai's own file-card style
 * rather than the plain "<> go · 18 lines" text-link this replaces.
 * Used by the inline chip in a message (MessageRow), the composer's
 * pending pasted-text-as-file chip (Composer), so both read as one
 * consistent design instead of two different treatments.
 */
export function FileCard({
  name,
  subtitle,
  kind,
  onClick,
  onRemove,
}: FileCardProps) {
  const body = (
    <>
      <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-[var(--login-accent)]/15 text-[var(--login-accent)]">
        {kind === "code" ? <CodeBracketsIcon /> : <FileIcon />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-left font-[family-name:var(--login-font-mono)] text-[13px] text-[var(--login-text)]">
          {name}
        </span>
        <span className="block truncate text-left text-[11.5px] text-[var(--login-text-muted)]">
          {subtitle}
        </span>
      </span>
      {onRemove && (
        <button
          type="button"
          aria-label={`Remove ${name}`}
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="shrink-0 rounded-md p-1 text-[var(--login-text-muted)] hover:bg-[var(--login-border-strong)] hover:text-[var(--login-text)]"
        >
          <CloseIcon />
        </button>
      )}
    </>
  );

  // inline-flex, not a block/w-full flex row: this sizes to its own
  // content (capped by max-w) whether it's sitting inline in the
  // composer's wrapping chip row or alone under a message's text — the
  // same "size to content" behavior the plain text-link chip it
  // replaces already had.
  const className =
    "inline-flex max-w-[300px] items-center gap-2.5 rounded-xl border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-2.5 py-2 hover:border-[var(--login-accent)]";

  if (onClick) {
    return (
      <button type="button" onClick={onClick} className={className}>
        {body}
      </button>
    );
  }
  return <div className={className}>{body}</div>;
}
