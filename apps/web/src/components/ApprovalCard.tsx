"use client";

import { WarnIcon } from "./icons";

export interface ApprovalCardProps {
  title: string;
  meta: string;
  onApprove: () => void;
  onReject: () => void;
  pending?: boolean;
}

/**
 * Approval-required timeline card — docs/design/room-mockup.html's
 * .card-approval, ported as a component only.
 *
 * IMPORTANT: this is not wired into the room timeline anywhere, and
 * that's deliberate, not an oversight. Nothing in this system today
 * lets an agent actually propose an action needing approval — chat
 * doesn't trigger structured actions (ADR-004's own existing decision),
 * so there is no real event type or backend code path that would ever
 * produce the data this card renders. Building a fake trigger just to
 * exercise this component would be UI theater over a capability that
 * doesn't exist (see ADR-004's second addendum, which calls this out
 * by name as real, deferred work needing its own design pass — how
 * would an agent actually propose an action? what does "approval"
 * change once granted, given chat doesn't trigger structured actions?
 * — not something to answer implicitly by shipping a demo).
 *
 * This component exists so that whenever a real trigger is designed and
 * built, the visual piece is already done and ready to render — pass it
 * real title/meta/handlers at that point, nothing here needs to change.
 */
export function ApprovalCard({
  title,
  meta,
  onApprove,
  onReject,
  pending,
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
        <WarnIcon />
        Approval needed
      </div>
      <div className="mb-1 text-[14.5px] font-medium text-[var(--login-text)]">
        {title}
      </div>
      <div className="mb-2.5 text-[12.5px] text-[var(--login-text-muted)]">
        {meta}
      </div>
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
    </div>
  );
}
