"use client";

export interface HandoffCardProps {
  status: string;
  fromName: string;
  toName: string;
  title: string;
  meta?: string;
}

/**
 * Handoff timeline card — docs/design/room-mockup.html's .card-handoff,
 * ported as a component only (the same treatment ApprovalCard already
 * got from .card-approval): the purple left border, the kind/status
 * head row, and the "From → To" flow line, instead of the generic
 * timeline card's bare label. Used for HANDOFF_REQUESTED/HANDOFF.REQUEST
 * — the one handoff transition with a real payload (summary, risks,
 * to_agent_id) to render from; HANDOFF_ACCEPTED/HANDOFF.ACCEPT still
 * falls back to the generic card, since internal/handoff's own
 * AcceptHandler publishes an empty payload today — nothing here to
 * enrich it with yet.
 */
export function HandoffCard({
  status,
  fromName,
  toName,
  title,
  meta,
}: HandoffCardProps) {
  return (
    // Same two-utility border split ApprovalCard's own comment explains:
    // border-l-[...] owns the left edge, the plain border/border-[...]
    // pair only the other three, so there's no stylesheet-order
    // collision between them.
    <div className="ml-[42px] rounded-[10px] border border-[var(--login-border-strong)] border-l-[3px] border-l-[var(--room-handoff-purple)] bg-[var(--room-handoff-purple)]/5 p-3.5 text-sm">
      <div className="mb-1.5 flex items-center justify-between">
        <span className="font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--room-handoff-purple)]">
          Handoff
        </span>
        <span className="rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-2 py-0.5 font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-secondary)]">
          {status}
        </span>
      </div>
      <div className="mb-2 flex items-center gap-2 text-[13px] text-[var(--login-text-secondary)]">
        <b className="font-semibold text-[var(--login-text)]">{fromName}</b>
        <span aria-hidden="true">→</span>
        <b className="font-semibold text-[var(--login-text)]">{toName}</b>
      </div>
      <div className="mb-1 text-[14.5px] font-medium text-[var(--login-text)]">
        {title}
      </div>
      {meta && (
        <div className="text-[12.5px] text-[var(--login-text-muted)]">
          {meta}
        </div>
      )}
    </div>
  );
}
