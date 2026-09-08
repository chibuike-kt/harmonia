"use client";

import { WarnIcon } from "./icons";

export interface ApprovalCardProps {
  title: string;
  meta: string;
  onApprove: () => void;
  onReject: () => void;
  /** A request to approve/reject is in flight — disables the buttons
   *  without changing anything else. Unrelated to `resolution`: this is
   *  transient (clears once the request finishes), resolution is
   *  permanent. */
  pending?: boolean;
  /** Set once a human has actually approved or rejected this proposal —
   *  swaps the amber "Approval needed" badge and the buttons for a
   *  plain resolved-state badge. Undefined means still open. This is
   *  the one thing that changes on resolution; title/meta and the
   *  card's own styling stay exactly the same, live or reconstructed
   *  from history alike. */
  resolution?: "approved" | "rejected";
}

/**
 * Approval-required timeline card — docs/design/room-mockup.html's
 * .card-approval, wired in for real (ADR-006 batch C: request_handoff's
 * pending proposals). The single, persistent representation of a
 * proposal everywhere it appears — live and reconstructed from history
 * alike — not just while it's pending: once resolved, the same card
 * stays in place with the same title/meta, only the badge and buttons
 * change (see `resolution`). Never disappears, never gets replaced by a
 * second, differently-styled block.
 */
export function ApprovalCard({
  title,
  meta,
  onApprove,
  onReject,
  pending,
  resolution,
}: ApprovalCardProps) {
  return (
    // Two distinct border concerns kept as separate utilities on
    // purpose: border-{color} sets all four sides' color, which would
    // silently fight border-l-{color} for the left side depending on
    // generated stylesheet order (the exact bug that broke the sidebar
    // once already this project — see Tooltip.tsx's own comment on it).
    // border-l-[3px] border-l-[...] together fully own the left side;
    // the plain border/border-[...] pair only ever applies to the other
    // three, so there's no property collision to resolve by luck.
    <div className="ml-[42px] rounded-[10px] border border-[var(--login-border-strong)] border-l-[3px] border-l-[var(--room-warn)] bg-[var(--room-warn)]/5 p-3.5 text-sm">
      <div className="mb-1.5 flex items-center gap-1.5 font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--room-warn)]">
        {resolution ? (
          <span
            className={
              resolution === "approved"
                ? "text-[var(--login-accent)]"
                : "text-[var(--login-text-muted)]"
            }
          >
            {resolution === "approved" ? "Approved" : "Rejected"}
          </span>
        ) : (
          <>
            <WarnIcon />
            Approval needed
          </>
        )}
      </div>
      <div className="mb-1 text-[14.5px] font-medium text-[var(--login-text)]">
        {title}
      </div>
      <div className="mb-2.5 text-[12.5px] text-[var(--login-text-muted)]">
        {meta}
      </div>
      {!resolution && (
        <div className="flex gap-2">
          <button
            type="button"
            disabled={pending}
            onClick={onApprove}
            className="rounded-lg bg-[var(--room-warn)] px-3.5 py-1.5 text-[12.5px] font-medium text-[var(--login-bg)] hover:bg-[#f0b658] disabled:opacity-50"
          >
            Approve
          </button>
          <button
            type="button"
            disabled={pending}
            onClick={onReject}
            className="rounded-lg border border-[var(--login-border-strong)] px-3.5 py-1.5 text-[12.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)] disabled:opacity-50"
          >
            Reject
          </button>
        </div>
      )}
    </div>
  );
}
