"use client";

import { FileGlyph } from "./ExplorerPanel";

export interface SearchMatch {
  path: string;
  line: number;
  text: string;
}

/**
 * The IDE's own real cross-file text search — VS Code's second real
 * activity-bar destination. Real recursive search against the real open
 * folder (internal/companion's own search_text), not a filename-only
 * quick-open (Ctrl+K already covers that): results are grouped by file,
 * each a real clickable line that opens that file and jumps to it.
 */
export function SearchPanel({
  query,
  onQueryChange,
  onSearch,
  results,
  searching,
  onOpenMatch,
}: {
  query: string;
  onQueryChange: (v: string) => void;
  onSearch: () => void;
  results: SearchMatch[];
  searching: boolean;
  onOpenMatch: (path: string, line: number) => void;
}) {
  const grouped = new Map<string, SearchMatch[]>();
  for (const m of results) {
    if (!grouped.has(m.path)) grouped.set(m.path, []);
    grouped.get(m.path)!.push(m);
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="px-3 pt-3 pb-2">
        <input
          autoFocus
          value={query}
          onChange={(e) => onQueryChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") onSearch();
          }}
          placeholder="Search across this project…"
          className="w-full rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2.5 py-1.5 text-[12.5px] text-[var(--ide-text)] outline-none placeholder:text-[var(--ide-text-muted)] focus:border-[var(--login-accent)]"
        />
      </div>
      <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto px-2 py-0.5">
        {searching && (
          <p className="px-2 py-2 text-[12px] text-[var(--ide-text-muted)]">
            Searching…
          </p>
        )}
        {!searching && query && results.length === 0 && (
          <p className="px-2 py-2 text-[12px] text-[var(--ide-text-muted)]">
            No results.
          </p>
        )}
        {[...grouped.entries()].map(([path, matches]) => (
          <div key={path} className="mb-1">
            <div className="flex items-center gap-1.5 truncate px-2 py-1 text-[11.5px] font-medium text-[var(--ide-text-secondary)]">
              <FileGlyph isDir={false} name={path.split("/").pop() ?? path} />
              <span className="truncate">{path}</span>
              <span className="shrink-0 text-[var(--ide-text-muted)]">
                ({matches.length})
              </span>
            </div>
            {matches.map((m) => (
              <button
                key={`${m.path}:${m.line}`}
                type="button"
                onClick={() => onOpenMatch(m.path, m.line)}
                className="flex w-full items-start gap-2 rounded-md px-2.5 py-1 text-left text-[12px] text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface)]"
              >
                <span className="shrink-0 font-[family-name:var(--login-font-mono)] text-[var(--ide-text-muted)]">
                  {m.line}
                </span>
                <span className="truncate font-[family-name:var(--login-font-mono)]">
                  {m.text}
                </span>
              </button>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
