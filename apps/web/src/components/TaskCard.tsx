"use client";

export interface TaskCardProps {
  status: "QUEUED" | "CLAIMED" | "COMPLETED";
  title: string;
  meta?: string;
}

/**
 * Task timeline card — docs/design/room-mockup.html's .card-task, ported
 * as a component (the same treatment ApprovalCard/HandoffCard already
 * got): the blue left border, the kind/status head row, an accent-
 * colored status badge while claimed (the closest real status this
 * codebase has to the mockup's illustrative "RUNNING"). One card per
 * task, its status updated in place as TASK_CLAIMED/TASK_COMPLETED
 * arrive — never a second card for the same task.
 */
export function TaskCard({ status, title, meta }: TaskCardProps) {
  return (
    <div className="ml-[42px] rounded-[10px] border border-[var(--login-border-strong)] border-l-[3px] border-l-[var(--room-task-blue)] bg-[var(--room-task-blue)]/5 p-3.5 text-sm">
      <div className="mb-1.5 flex items-center justify-between">
        <span className="font-[family-name:var(--login-font-mono)] text-[11px] uppercase tracking-wide text-[var(--room-task-blue)]">
          Task
        </span>
        <span
          className={
            status === "CLAIMED"
              ? "rounded-full border border-[var(--login-accent)]/35 bg-[var(--login-surface-2)] px-2 py-0.5 font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-accent)]"
              : "rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-2 py-0.5 font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-secondary)]"
          }
        >
          {status}
        </span>
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
