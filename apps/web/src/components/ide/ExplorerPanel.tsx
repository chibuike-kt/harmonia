"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronRightIcon } from "@/components/icons";
import { Tooltip } from "@/components/Tooltip";
import { fileIconUrl, folderIconUrl } from "@/lib/fileIcons";

export interface DirEntry {
  name: string;
  path: string;
  is_dir: boolean;
}

export function FileGlyph({
  isDir,
  name,
  expanded,
}: {
  isDir: boolean;
  name: string;
  expanded?: boolean;
}) {
  const src = isDir ? folderIconUrl(name, !!expanded) : fileIconUrl(name);
  // eslint-disable-next-line @next/next/no-img-element
  return <img src={src} alt="" width={16} height={16} className="shrink-0" />;
}

type ContextMenuState = {
  x: number;
  y: number;
  path: string;
  isDir: boolean;
} | null;
type CreatingState = { parentDir: string; kind: "file" | "folder" } | null;

/**
 * The IDE's own real Explorer — the file tree plus a real, standard
 * right-click context menu (New File, New Folder, Rename, Delete) and
 * real inline naming for New File/Folder and Rename, matching VS Code's
 * own convention exactly rather than a modal dialog for any of the four.
 * Every real mutation goes through the parent page's own companion
 * WebSocket (via the on* callbacks); this component owns only its own
 * transient UI state (which menu/input is open right now).
 */
export function ExplorerPanel({
  folderName,
  dirListings,
  expanded,
  activePath,
  isLive,
  onToggleDir,
  onOpenFile,
  onCreateFile,
  onCreateFolder,
  onRename,
  onDelete,
}: {
  folderName: string;
  dirListings: Record<string, DirEntry[]>;
  expanded: Set<string>;
  activePath: string | null;
  isLive: (path: string) => boolean;
  onToggleDir: (path: string) => void;
  onOpenFile: (path: string) => void;
  onCreateFile: (parentDir: string, name: string) => void;
  onCreateFolder: (parentDir: string, name: string) => void;
  onRename: (oldPath: string, newName: string) => void;
  onDelete: (path: string) => void;
}) {
  const [contextMenu, setContextMenu] = useState<ContextMenuState>(null);
  const [creating, setCreating] = useState<CreatingState>(null);
  const [creatingValue, setCreatingValue] = useState("");
  const [renaming, setRenaming] = useState<string | null>(null);
  const [renamingValue, setRenamingValue] = useState("");
  const inlineInputRef = useRef<HTMLInputElement>(null);
  const contextMenuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (creating || renaming) inlineInputRef.current?.focus();
  }, [creating, renaming]);

  useEffect(() => {
    if (!contextMenu) return;
    // A mousedown *inside* the menu (i.e. on one of its own real
    // buttons) must not close it before the matching click ever fires —
    // mousedown always fires before click, so closing unconditionally
    // here removes the button from the DOM in between, and the click
    // that was supposed to run New File/Rename/Delete never lands at
    // all. Found live: every context-menu action silently doing
    // nothing. Only a real outside click should close it.
    const close = (e: Event) => {
      if (contextMenuRef.current?.contains(e.target as Node)) return;
      setContextMenu(null);
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("blur", close);
    };
  }, [contextMenu]);

  const openMenuFor = (e: React.MouseEvent, path: string, isDir: boolean) => {
    e.preventDefault();
    e.stopPropagation();
    setContextMenu({ x: e.clientX, y: e.clientY, path, isDir });
  };

  const startCreating = (parentDir: string, kind: "file" | "folder") => {
    setContextMenu(null);
    setCreating({ parentDir, kind });
    setCreatingValue("");
    if (!expanded.has(parentDir)) onToggleDir(parentDir);
  };

  const submitCreating = () => {
    if (!creating) return;
    const name = creatingValue.trim();
    if (name) {
      if (creating.kind === "file") onCreateFile(creating.parentDir, name);
      else onCreateFolder(creating.parentDir, name);
    }
    setCreating(null);
  };

  const startRenaming = (path: string) => {
    setContextMenu(null);
    setRenaming(path);
    setRenamingValue(path.split("/").pop() ?? path);
  };

  const submitRenaming = () => {
    if (!renaming) return;
    const name = renamingValue.trim();
    if (name && name !== renaming.split("/").pop()) onRename(renaming, name);
    setRenaming(null);
  };

  const renderInlineInput = (
    value: string,
    onChange: (v: string) => void,
    onSubmit: () => void,
    onCancel: () => void,
    depth: number,
  ) => (
    <input
      ref={inlineInputRef}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onBlur={onSubmit}
      onKeyDown={(e) => {
        if (e.key === "Enter") onSubmit();
        else if (e.key === "Escape") onCancel();
      }}
      style={{ paddingLeft: `${depth * 14 + 8}px` }}
      className="w-full rounded-md border border-[var(--login-accent)] bg-[var(--ide-surface)] py-1 pr-2 text-[13px] text-[var(--ide-text)] outline-none"
    />
  );

  const renderEntries = (dirPath: string, depth: number): React.ReactNode => {
    const entries = dirListings[dirPath];
    const creatingHere = creating?.parentDir === dirPath;
    return (
      <>
        {entries?.map((entry) => {
          const live = isLive(entry.path);
          if (renaming === entry.path) {
            return (
              <div key={entry.path} className="px-0 py-0.5">
                {renderInlineInput(
                  renamingValue,
                  setRenamingValue,
                  submitRenaming,
                  () => setRenaming(null),
                  depth,
                )}
              </div>
            );
          }
          if (entry.is_dir) {
            const isOpen = expanded.has(entry.path);
            return (
              <div key={entry.path}>
                <button
                  type="button"
                  onClick={() => onToggleDir(entry.path)}
                  onContextMenu={(e) => openMenuFor(e, entry.path, true)}
                  style={{ paddingLeft: `${depth * 14 + 4}px` }}
                  className="flex w-full items-center gap-1.5 rounded-md py-1 pr-2 text-left text-[13px] text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface)]"
                >
                  <span
                    className={`flex shrink-0 transition-transform ${isOpen ? "rotate-90" : ""}`}
                  >
                    <ChevronRightIcon />
                  </span>
                  <FileGlyph isDir name={entry.name} expanded={isOpen} />
                  <span className="truncate">{entry.name}</span>
                </button>
                {isOpen && renderEntries(entry.path, depth + 1)}
              </div>
            );
          }
          return (
            <button
              key={entry.path}
              type="button"
              onClick={() => onOpenFile(entry.path)}
              onContextMenu={(e) => openMenuFor(e, entry.path, false)}
              style={{ paddingLeft: `${depth * 14 + 8}px` }}
              className={`flex w-full items-center gap-1.5 rounded-md py-1 pr-2 text-left text-[13px] ${
                activePath === entry.path
                  ? "bg-[rgba(76,211,194,.1)] text-[var(--ide-text)]"
                  : "text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface)]"
              }`}
            >
              <FileGlyph isDir={false} name={entry.name} />
              <span className="truncate">{entry.name}</span>
              {live && (
                <span className="ml-auto h-[5px] w-[5px] shrink-0 rounded-full bg-[var(--login-accent)] shadow-[0_0_4px_rgba(76,211,194,.28)]" />
              )}
            </button>
          );
        })}
        {creatingHere && (
          <div className="px-0 py-0.5">
            {renderInlineInput(
              creatingValue,
              setCreatingValue,
              submitCreating,
              () => setCreating(null),
              depth,
            )}
          </div>
        )}
      </>
    );
  };

  return (
    <div
      className="flex h-full min-h-0 flex-col"
      onContextMenu={(e) => openMenuFor(e, "", true)}
    >
      <div className="flex items-center justify-between px-3 pt-3 pb-2">
        <span className="truncate font-[family-name:var(--login-font-mono)] text-[10.5px] tracking-wide text-[var(--ide-text-muted)] uppercase">
          {folderName}
        </span>
        <div className="flex items-center gap-0.5">
          <Tooltip label="New File" side="top" align="end">
            <button
              type="button"
              onClick={() => startCreating("", "file")}
              className="flex h-5 w-5 items-center justify-center rounded text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface)] hover:text-[var(--ide-text)]"
            >
              <svg
                width="13"
                height="13"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
              >
                <path d="M4 2h5l3 3v9H4z" />
                <path d="M8 7v4M6 9h4" />
              </svg>
            </button>
          </Tooltip>
          <Tooltip label="New Folder" side="top" align="end">
            <button
              type="button"
              onClick={() => startCreating("", "folder")}
              className="flex h-5 w-5 items-center justify-center rounded text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface)] hover:text-[var(--ide-text)]"
            >
              <svg
                width="13"
                height="13"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
              >
                <path d="M2 4h4l1.5 1.5H14V13H2z" />
                <path d="M8 8v3M6.5 9.5h3" />
              </svg>
            </button>
          </Tooltip>
        </div>
      </div>
      <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto px-2 py-0.5">
        {renderEntries("", 0)}
      </div>

      {contextMenu && (
        <div
          ref={contextMenuRef}
          style={{ left: contextMenu.x, top: contextMenu.y }}
          className="fixed z-50 min-w-[160px] rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] p-1 text-[12.5px] shadow-xl"
          onClick={(e) => e.stopPropagation()}
        >
          <button
            type="button"
            onClick={() =>
              startCreating(
                contextMenu.isDir
                  ? contextMenu.path
                  : parentOf(contextMenu.path),
                "file",
              )
            }
            className="flex w-full items-center rounded px-2 py-1.5 text-left text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[#0a0c11]"
          >
            New File
          </button>
          <button
            type="button"
            onClick={() =>
              startCreating(
                contextMenu.isDir
                  ? contextMenu.path
                  : parentOf(contextMenu.path),
                "folder",
              )
            }
            className="flex w-full items-center rounded px-2 py-1.5 text-left text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[#0a0c11]"
          >
            New Folder
          </button>
          {contextMenu.path !== "" && (
            <>
              <div className="my-1 h-px bg-[var(--ide-border)]" />
              <button
                type="button"
                onClick={() => startRenaming(contextMenu.path)}
                className="flex w-full items-center rounded px-2 py-1.5 text-left text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[#0a0c11]"
              >
                Rename
              </button>
              <button
                type="button"
                onClick={() => {
                  setContextMenu(null);
                  onDelete(contextMenu.path);
                }}
                className="flex w-full items-center rounded px-2 py-1.5 text-left text-[var(--room-warn)] hover:bg-[var(--room-warn)] hover:text-[var(--ide-surface)]"
              >
                Delete
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function parentOf(path: string): string {
  const idx = path.lastIndexOf("/");
  return idx < 0 ? "" : path.slice(0, idx);
}
