"use client";

import { Tooltip } from "@/components/Tooltip";

export type SidebarView = "explorer" | "search" | "scm";

function ExplorerIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
    >
      <path d="M4 5h6l2 2.5h8V19H4z" />
    </svg>
  );
}
function SearchIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
    >
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="M20 20l-5-5" />
    </svg>
  );
}
function SourceControlIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
    >
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="6" cy="18" r="2.5" />
      <circle cx="18" cy="10" r="2.5" />
      <path d="M6 8.5V15.5M6 8.5c0 4 3 4 8 4h2" />
    </svg>
  );
}

const ITEMS: {
  id: SidebarView;
  label: string;
  Icon: () => React.ReactElement;
}[] = [
  { id: "explorer", label: "Explorer", Icon: ExplorerIcon },
  { id: "search", label: "Search", Icon: SearchIcon },
  { id: "scm", label: "Source Control", Icon: SourceControlIcon },
];

/**
 * The IDE's own real activity bar — Explorer / Search / Source Control,
 * VS Code's own real convention: a fixed icon rail down the left edge
 * that switches which panel the resizable side panel shows, entirely
 * separate from (and replacing, on this page) Harmonia's own app-level
 * navigation sidebar.
 */
export function ActivityBar({
  active,
  onChange,
}: {
  active: SidebarView;
  onChange: (view: SidebarView) => void;
}) {
  return (
    <div className="flex w-11 shrink-0 flex-col items-center gap-1 border-r border-[var(--ide-border)] bg-[var(--ide-bg-sidebar)] py-2">
      {ITEMS.map(({ id, label, Icon }) => (
        <Tooltip key={id} label={label} side="right">
          <button
            type="button"
            onClick={() => onChange(id)}
            className={`relative flex h-10 w-10 items-center justify-center rounded-md ${
              active === id
                ? "text-[var(--ide-text)]"
                : "text-[var(--ide-text-muted)] hover:text-[var(--ide-text-secondary)]"
            }`}
          >
            {active === id && (
              <span className="absolute left-0 h-6 w-[2px] rounded-full bg-[var(--login-accent)]" />
            )}
            <Icon />
          </button>
        </Tooltip>
      ))}
    </div>
  );
}
