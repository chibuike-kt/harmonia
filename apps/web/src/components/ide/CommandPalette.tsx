"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { OrbMark } from "@/components/OrbMark";
import { FileGlyph } from "./ExplorerPanel";

export interface CommandPaletteFile {
  path: string;
  name: string;
}

export interface CommandPaletteAction {
  id: string;
  label: string;
  shortcut?: string;
  run: () => void;
}

/**
 * The real, working command palette (Ctrl+K) — centered, blurred
 * backdrop, genuinely filtering as the human types, matching
 * docs/design/harmonia-ide-mockup.html. "Ask an agent about the open
 * file" is a first-class result alongside file-jumping and actions, not
 * a bolted-on extra row, so it filters and keyboard-navigates exactly
 * like every other result.
 */
export function CommandPalette({
  open,
  onClose,
  files,
  actions,
  activeFileName,
  onAskAgent,
  onOpenFile,
  agentName,
}: {
  open: boolean;
  onClose: () => void;
  files: CommandPaletteFile[];
  actions: CommandPaletteAction[];
  activeFileName: string | null;
  agentName: string | null;
  onAskAgent: (question: string) => void;
  onOpenFile: (path: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      // Resetting for a fresh open — same fetch/connect-on-mount
      // justification this codebase already applies to this lint rule.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setQuery("");
      setSelected(0);
      // Focus needs a tick — the input isn't in the DOM yet the instant
      // `open` flips true within the same render.
      const id = setTimeout(() => inputRef.current?.focus(), 0);
      return () => clearTimeout(id);
    }
  }, [open]);

  type Row =
    | { kind: "ask"; label: string; run: () => void }
    | { kind: "file"; label: string; name: string; run: () => void }
    | { kind: "action"; label: string; shortcut?: string; run: () => void };

  const rows = useMemo<{ section: string; items: Row[] }[]>(() => {
    const q = query.trim().toLowerCase();
    const sections: { section: string; items: Row[] }[] = [];

    if (agentName && activeFileName) {
      const askLabel = q
        ? `Ask ${agentName} about ${activeFileName}: "${query}"`
        : `Ask ${agentName} about ${activeFileName}`;
      sections.push({
        section: "Ask",
        items: [
          {
            kind: "ask",
            label: askLabel,
            run: () => onAskAgent(q || `about ${activeFileName}`),
          },
        ],
      });
    }

    const matchingFiles = files.filter((f) =>
      f.name.toLowerCase().includes(q),
    );
    if (matchingFiles.length > 0) {
      sections.push({
        section: "Go to File",
        items: matchingFiles.slice(0, 8).map((f) => ({
          kind: "file",
          label: f.name,
          name: f.name,
          run: () => onOpenFile(f.path),
        })),
      });
    }

    const matchingActions = actions.filter((a) =>
      a.label.toLowerCase().includes(q),
    );
    if (matchingActions.length > 0) {
      sections.push({
        section: "Actions",
        items: matchingActions.map((a) => ({
          kind: "action",
          label: a.label,
          shortcut: a.shortcut,
          run: a.run,
        })),
      });
    }

    return sections;
  }, [query, files, actions, activeFileName, agentName, onAskAgent, onOpenFile]);

  const flat = useMemo(() => rows.flatMap((s) => s.items), [rows]);
  // Clamped directly during render rather than via an effect that
  // re-fires setSelected — the filtered list shrinking below the
  // previously selected index is exactly the case react.dev's own
  // guidance says to derive, not synchronize.
  const selectedIndex = selected < flat.length ? selected : 0;

  if (!open) return null;

  const runSelected = () => {
    const row = flat[selectedIndex];
    if (row) {
      row.run();
      onClose();
    }
  };

  return (
    <div
      className="fixed inset-0 z-[100] flex items-start justify-center bg-[rgba(6,8,11,.6)] pt-[120px] backdrop-blur-[3px]"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
        className="w-[560px] max-w-[90vw] overflow-hidden rounded-xl border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] shadow-[0_24px_64px_rgba(0,0,0,.6),0_0_0_1px_rgba(76,211,194,.28)]"
      >
        <div className="flex items-center gap-2.5 border-b border-[var(--ide-border)] px-4 py-3.5">
          <OrbMark size={18} />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelected(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setSelected((s) => Math.min(s + 1, flat.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSelected((s) => Math.max(s - 1, 0));
              } else if (e.key === "Enter") {
                e.preventDefault();
                runSelected();
              } else if (e.key === "Escape") {
                e.preventDefault();
                onClose();
              }
            }}
            placeholder="Ask an agent, or jump to a file…"
            className="flex-1 bg-transparent text-[14.5px] text-[var(--ide-text)] outline-none placeholder:text-[var(--ide-text-muted)]"
          />
        </div>
        <div className="no-scrollbar max-h-[320px] overflow-y-auto p-1.5">
          {flat.length === 0 && (
            <p className="px-2.5 py-4 text-center text-[13px] text-[var(--ide-text-muted)]">
              No matches.
            </p>
          )}
          {rows.map((section) => (
            <div key={section.section}>
              <div className="px-2.5 pt-2 pb-1 font-[family-name:var(--login-font-mono)] text-[10.5px] tracking-wide text-[var(--ide-text-muted)] uppercase">
                {section.section}
              </div>
              {section.items.map((item) => {
                const flatIndex = flat.indexOf(item);
                const isSelected = flatIndex === selectedIndex;
                return (
                  <div
                    key={item.label}
                    role="option"
                    aria-selected={isSelected}
                    onMouseEnter={() => setSelected(flatIndex)}
                    onClick={() => {
                      item.run();
                      onClose();
                    }}
                    className={`flex cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-[9px] text-[13.5px] ${
                      isSelected
                        ? "bg-[rgba(76,211,194,.1)] text-[var(--ide-text)]"
                        : "text-[var(--ide-text-secondary)]"
                    }`}
                  >
                    {item.kind === "file" ? (
                      <FileGlyph isDir={false} name={item.name} />
                    ) : (
                      <span className="inline-block w-[15px]" />
                    )}
                    <span className="truncate">{item.label}</span>
                    {item.kind === "action" && item.shortcut && (
                      <span className="ml-auto font-[family-name:var(--login-font-mono)] text-[10.5px] text-[var(--ide-text-muted)]">
                        {item.shortcut}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
