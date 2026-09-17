"use client";

import { useMemo, useState } from "react";
import { diffLines } from "diff";

export interface FileEditProposal {
  proposalId: string;
  path: string;
  agentName: string;
  provider?: string;
  oldContent: string;
  newContent: string;
}

type RenderRow =
  | { kind: "context"; hunkId: number | null; oldLine: number; newLine: number; text: string }
  | { kind: "removed"; hunkId: number; oldLine: number; text: string }
  | { kind: "added"; hunkId: number; newLine: number; text: string };

interface Hunk {
  id: number;
  removedLines: string[];
  addedLines: string[];
}

/**
 * Groups jsdiff's flat diffLines() output into real per-hunk blocks — a
 * "removed" change immediately followed by an "added" one is one real
 * replace hunk (old lines -> new lines), not two independent ones; a
 * lone removed or added run is a pure deletion or insertion hunk. Also
 * builds the flat row list the diff pane actually renders, with real
 * old/new line numbers threaded through — not display-only, the same
 * numbers a human reviewing against the real file would expect.
 */
function computeHunks(oldContent: string, newContent: string): { rows: RenderRow[]; hunks: Hunk[] } {
  const changes = diffLines(oldContent, newContent);
  const rows: RenderRow[] = [];
  const hunks: Hunk[] = [];
  let oldLine = 1;
  let newLine = 1;
  let hunkId = 0;

  for (let i = 0; i < changes.length; i++) {
    const change = changes[i];
    const lines = change.value.split("\n");
    if (lines[lines.length - 1] === "") lines.pop();

    if (!change.added && !change.removed) {
      for (const text of lines) {
        rows.push({ kind: "context", hunkId: null, oldLine, newLine, text });
        oldLine++;
        newLine++;
      }
      continue;
    }

    if (change.removed) {
      const next = changes[i + 1];
      const id = hunkId++;
      const removedLines = lines;
      let addedLines: string[] = [];
      for (const text of removedLines) {
        rows.push({ kind: "removed", hunkId: id, oldLine, text });
        oldLine++;
      }
      if (next?.added) {
        const addedRaw = next.value.split("\n");
        if (addedRaw[addedRaw.length - 1] === "") addedRaw.pop();
        addedLines = addedRaw;
        for (const text of addedLines) {
          rows.push({ kind: "added", hunkId: id, newLine, text });
          newLine++;
        }
        i++; // consumed the paired "added" change
      }
      hunks.push({ id, removedLines, addedLines });
      continue;
    }

    // A lone "added" run (pure insertion, no preceding removed run).
    const id = hunkId++;
    for (const text of lines) {
      rows.push({ kind: "added", hunkId: id, newLine, text });
      newLine++;
    }
    hunks.push({ id, removedLines: [], addedLines: lines });
  }

  return { rows, hunks };
}

/** The real merged content for a set of per-hunk decisions — what
 *  actually gets written once the human is done resolving. An
 *  undecided hunk is treated as rejected (its old lines kept): nothing
 *  is applied that wasn't explicitly accepted. */
function mergeContent(oldContent: string, newContent: string, hunks: Hunk[], decisions: Record<number, "accepted" | "rejected">): string {
  const changes = diffLines(oldContent, newContent);
  const out: string[] = [];
  let hunkIdx = 0;

  for (let i = 0; i < changes.length; i++) {
    const change = changes[i];
    if (!change.added && !change.removed) {
      out.push(change.value);
      continue;
    }
    if (change.removed) {
      const hunk = hunks[hunkIdx];
      const next = changes[i + 1];
      const accepted = decisions[hunk.id] === "accepted";
      if (next?.added) {
        out.push(accepted ? next.value : change.value);
        i++;
      } else if (!accepted) {
        out.push(change.value);
      }
      hunkIdx++;
      continue;
    }
    // Lone "added" (pure insertion).
    const hunk = hunks[hunkIdx];
    if (decisions[hunk.id] === "accepted") out.push(change.value);
    hunkIdx++;
  }
  return out.join("");
}

/**
 * The pending-approval diff view — docs/design/harmonia-ide-mockup.html's
 * presence-gone fallback, rendered as a full view takeover rather than a
 * dismissible badge (design decision, see this component's own report):
 * ADR-010's whole safety model rests on a human genuinely reviewing what
 * an agent did while unwatched, and a low-visibility badge risks that
 * review never actually happening. Real per-hunk accept/reject, a real
 * ruler strip, and Accept All/Reject All — reused as-is for any future
 * agent-proposed edit (per the design overhaul's own instruction), not a
 * one-off UI for this proposal type alone.
 */
export function DiffView({
  proposal,
  onResolved,
}: {
  proposal: FileEditProposal;
  onResolved: (result: { approved: boolean; error?: string }) => void;
}) {
  const { rows, hunks } = useMemo(
    () => computeHunks(proposal.oldContent, proposal.newContent),
    [proposal.oldContent, proposal.newContent],
  );
  const [decisions, setDecisions] = useState<Record<number, "accepted" | "rejected">>({});
  const [busy, setBusy] = useState(false);

  const allDecided = hunks.length > 0 && hunks.every((h) => decisions[h.id]);
  const anyDecided = Object.keys(decisions).length > 0;

  const approve = async (contentBase64Override?: string) => {
    setBusy(true);
    try {
      const res = await fetch(`${process.env.NEXT_PUBLIC_API_BASE_URL}/v1/action_proposals/${proposal.proposalId}/approve`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(contentBase64Override ? { content_base64: contentBase64Override } : {}),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { error?: string };
        onResolved({ approved: true, error: body.error ?? `approve failed: ${res.status}` });
        return;
      }
      onResolved({ approved: true });
    } finally {
      setBusy(false);
    }
  };

  const reject = async () => {
    setBusy(true);
    try {
      const res = await fetch(`${process.env.NEXT_PUBLIC_API_BASE_URL}/v1/action_proposals/${proposal.proposalId}/reject`, {
        method: "POST",
        credentials: "include",
      });
      if (!res.ok) {
        onResolved({ approved: false, error: `reject failed: ${res.status}` });
        return;
      }
      onResolved({ approved: false });
    } finally {
      setBusy(false);
    }
  };

  const acceptAll = () => void approve();
  const rejectAll = () => void reject();

  const applyResolved = () => {
    const merged = mergeContent(proposal.oldContent, proposal.newContent, hunks, decisions);
    void approve(btoa(unescape(encodeURIComponent(merged))));
  };

  const decide = (hunkId: number, decision: "accepted" | "rejected") =>
    setDecisions((prev) => ({ ...prev, [hunkId]: decision }));

  // Ruler ticks: one per hunk-bearing row, positioned proportionally
  // down the diff pane — a real, if approximate, map of where the real
  // changes are in the file, matching the mockup's own ruler strip.
  const rulerTicks = rows
    .map((row, i) => ({ row, i }))
    .filter(({ row }) => row.kind !== "context")
    .map(({ row, i }) => ({
      kind: row.kind,
      topPercent: (i / Math.max(rows.length - 1, 1)) * 100,
    }));

  return (
    <div className="flex min-w-0 flex-1 flex-col bg-[var(--ide-bg-panel)]">
      <div className="flex shrink-0 items-center justify-between border-b border-[rgba(232,163,61,.25)] bg-[rgba(232,163,61,.08)] px-4 py-2.5">
        <div className="flex items-center gap-2 text-[12.5px] text-[var(--ide-text)]">
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="#E8A33D" strokeWidth="1.6" className="shrink-0">
            <path d="M8 1.5l5 2v4c0 3.5-2.2 5.8-5 7-2.8-1.2-5-3.5-5-7v-4l5-2z" />
          </svg>
          <span>
            <b>{proposal.agentName}</b> proposed changes to <b>{proposal.path}</b> while no one was watching —
            review before they&apos;re applied.
          </span>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            disabled={busy}
            onClick={rejectAll}
            className="rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] px-3 py-1 text-[11.5px] font-medium text-[var(--ide-text-secondary)] disabled:opacity-50"
          >
            Reject all
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={acceptAll}
            className="rounded-md bg-[var(--login-accent)] px-3 py-1 text-[11.5px] font-medium text-[var(--ide-bg)] disabled:opacity-50"
          >
            Accept all
          </button>
        </div>
      </div>

      <div className="no-scrollbar flex min-h-0 flex-1 overflow-auto font-[family-name:var(--login-font-mono)] text-[13px] leading-[1.7]">
        <div className="flex-1 py-3">
          {(() => {
            const out: React.ReactNode[] = [];
            let i = 0;
            while (i < rows.length) {
              const row = rows[i];
              if (row.kind === "context") {
                out.push(
                  <div key={i} className="flex items-center">
                    <span className="w-5 shrink-0 text-center text-[var(--ide-text-muted)]" />
                    <span className="w-10 shrink-0 pr-3.5 text-right text-[var(--ide-text-muted)] opacity-55">
                      {row.oldLine}
                    </span>
                    <span className="whitespace-pre text-[var(--ide-text-secondary)]">{row.text || " "}</span>
                  </div>,
                );
                i++;
                continue;
              }
              const hunkId = row.hunkId;
              const hunk = hunks[hunkId];
              const decision = decisions[hunkId];
              while (i < rows.length && rows[i].kind !== "context" && (rows[i] as { hunkId: number }).hunkId === hunkId) {
                const r = rows[i];
                if (r.kind === "removed") {
                  out.push(
                    <div key={i} className={`flex items-center bg-[rgba(240,112,91,.08)] ${decision === "accepted" ? "opacity-35" : ""}`}>
                      <span className="w-5 shrink-0 text-center text-[#F0705B]">−</span>
                      <span className="w-10 shrink-0 pr-3.5 text-right text-[var(--ide-text-muted)] opacity-55">{r.oldLine}</span>
                      <span className="whitespace-pre text-[#F0705B] opacity-85 line-through decoration-[rgba(240,112,91,.4)]">
                        {r.text || " "}
                      </span>
                    </div>,
                  );
                } else {
                  out.push(
                    <div key={i} className={`flex items-center bg-[rgba(76,211,194,.08)] ${decision === "rejected" ? "opacity-35" : ""}`}>
                      <span className="w-5 shrink-0 text-center text-[var(--login-accent)]">+</span>
                      <span className="w-10 shrink-0 pr-3.5 text-right text-[var(--ide-text-muted)] opacity-55">{r.newLine}</span>
                      <span className="whitespace-pre text-[var(--ide-text)]">{r.text || " "}</span>
                    </div>,
                  );
                }
                i++;
              }
              out.push(
                <div
                  key={`hunk-${hunkId}`}
                  className={`my-1.5 flex items-center gap-1 px-2.5 ${decision ? "pointer-events-none opacity-35" : ""}`}
                >
                  <button
                    type="button"
                    onClick={() => decide(hunkId, "accepted")}
                    className="rounded-[5px] border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2 py-[2px] text-[10.5px] text-[var(--ide-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-accent)]"
                  >
                    ✓ Accept hunk
                  </button>
                  <button
                    type="button"
                    onClick={() => decide(hunkId, "rejected")}
                    className="rounded-[5px] border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2 py-[2px] text-[10.5px] text-[var(--ide-text-secondary)] hover:border-[#F0705B] hover:text-[#F0705B]"
                  >
                    ✕ Reject hunk
                  </button>
                  {hunk.removedLines.length === 0 && (
                    <span className="text-[10.5px] text-[var(--ide-text-muted)]">(insertion)</span>
                  )}
                </div>,
              );
            }
            return out;
          })()}
        </div>
        <div className="relative w-3 shrink-0 border-l border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)]">
          {rulerTicks.map((tick, i) => (
            <div
              key={i}
              className="absolute left-[2px] h-[3px] w-2 rounded-[1px]"
              style={{
                top: `${tick.topPercent}%`,
                background: tick.kind === "removed" ? "#F0705B" : "var(--login-accent)",
              }}
            />
          ))}
        </div>
      </div>

      {hunks.length > 0 && (
        <div className="flex shrink-0 items-center justify-between border-t border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)] px-4 py-2 text-[11.5px] text-[var(--ide-text-muted)]">
          <span>
            {Object.keys(decisions).length} of {hunks.length} hunks resolved
          </span>
          <button
            type="button"
            disabled={!allDecided || busy}
            onClick={applyResolved}
            className="rounded-md bg-[var(--login-accent)] px-3 py-1 text-[11.5px] font-medium text-[var(--ide-bg)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            {anyDecided ? "Apply resolved changes" : "Resolve every hunk to apply"}
          </button>
        </div>
      )}
    </div>
  );
}
