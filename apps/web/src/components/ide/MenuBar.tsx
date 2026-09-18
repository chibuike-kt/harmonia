"use client";

import { useEffect, useRef, useState } from "react";
import { OrbMark } from "@/components/OrbMark";
import { Tooltip } from "@/components/Tooltip";
import { AgentAvatarGlyph } from "@/components/providerLogos";
import { AddAgentMenu, type AddedAgent } from "@/components/AddAgentMenu";

export interface PresenceAgent {
  id: string;
  name: string;
  provider: string;
  active: boolean;
}

function DropdownItem({
  label,
  shortcut,
  checked,
  disabled,
  onSelect,
}: {
  label: string;
  shortcut?: string;
  checked?: boolean;
  disabled?: boolean;
  onSelect?: () => void;
}) {
  return (
    <div
      role="menuitem"
      aria-disabled={disabled}
      onClick={disabled ? undefined : onSelect}
      className={`flex items-center justify-between gap-6 rounded-md px-2.5 py-[7px] text-[13px] ${
        disabled
          ? "cursor-default text-[var(--ide-text-muted)]"
          : "cursor-pointer text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[var(--ide-bg)]"
      }`}
    >
      <span className="flex items-center gap-2">
        {checked && <span className="text-[var(--login-accent)]">✓</span>}
        {label}
      </span>
      {shortcut && (
        <span className="font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--ide-text-muted)]">
          {shortcut}
        </span>
      )}
    </div>
  );
}

function DropdownDivider() {
  return <div className="my-[5px] h-px bg-[var(--ide-border)]" />;
}

interface MenuDef {
  key: string;
  label: string;
  items: React.ReactNode;
}

/**
 * The IDE's real top menu bar — File/Edit/Selection/View/Go/Run/
 * Terminal/Help, matching VS Code's own structure, trimmed to what's
 * actually real in Harmonia (docs/design/harmonia-ide-mockup.html). Every
 * item here calls a real callback the page wires to a real action — this
 * component owns only which dropdown is open, never the actions
 * themselves.
 */
export function MenuBar({
  folderOpen,
  autoSave,
  explorerOpen,
  panelOpen,
  canUndo,
  canRedo,
  humanInitials,
  agents,
  roomId,
  onAgentAdded,
  onOpenFolder,
  onCloseFolder,
  onSave,
  onSaveAs,
  onToggleAutoSave,
  onUndo,
  onRedo,
  onFind,
  onSelectAll,
  onToggleExplorer,
  onTogglePanel,
  onOpenCmdk,
  onNewTerminal,
  onGoToFile,
  followingAgentId,
  onToggleFollow,
  onStopFollowing,
}: {
  folderOpen: boolean;
  autoSave: boolean;
  explorerOpen: boolean;
  panelOpen: boolean;
  canUndo: boolean;
  canRedo: boolean;
  humanInitials: string;
  agents: PresenceAgent[];
  /** The IDE's own paired room (ADR-010) — null until it resolves, in
   *  which case Add Agent simply doesn't render yet rather than pointing
   *  at a room that doesn't exist. */
  roomId: string | null;
  onAgentAdded: (agent: AddedAgent) => void;
  followingAgentId: string | null;
  onToggleFollow: (agentId: string) => void;
  onStopFollowing: () => void;
  onOpenFolder: () => void;
  onCloseFolder: () => void;
  onSave: () => void;
  onSaveAs: () => void;
  onToggleAutoSave: () => void;
  onUndo: () => void;
  onRedo: () => void;
  onFind: () => void;
  onSelectAll: () => void;
  onToggleExplorer: () => void;
  onTogglePanel: () => void;
  onOpenCmdk: () => void;
  onNewTerminal: () => void;
  onGoToFile: () => void;
}) {
  const [openMenu, setOpenMenu] = useState<string | null>(null);
  const barRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!openMenu) return;
    const onClickAway = (e: MouseEvent) => {
      if (!barRef.current?.contains(e.target as Node)) setOpenMenu(null);
    };
    document.addEventListener("click", onClickAway);
    return () => document.removeEventListener("click", onClickAway);
  }, [openMenu]);

  const menus: MenuDef[] = [
    {
      key: "file",
      label: "File",
      items: (
        <>
          <DropdownItem
            label="Open Folder…"
            shortcut="Ctrl+O"
            onSelect={onOpenFolder}
          />
          <DropdownItem
            label="Close Folder"
            shortcut="Ctrl+K F"
            disabled={!folderOpen}
            onSelect={onCloseFolder}
          />
          <DropdownDivider />
          <DropdownItem
            label="Save"
            shortcut="Ctrl+S"
            disabled={!folderOpen}
            onSelect={onSave}
          />
          <DropdownItem
            label="Save As…"
            shortcut="Ctrl+Shift+S"
            disabled={!folderOpen}
            onSelect={onSaveAs}
          />
          <DropdownDivider />
          <DropdownItem
            label="Auto Save"
            checked={autoSave}
            onSelect={onToggleAutoSave}
          />
        </>
      ),
    },
    {
      key: "edit",
      label: "Edit",
      items: (
        <>
          <DropdownItem
            label="Undo"
            shortcut="Ctrl+Z"
            disabled={!canUndo}
            onSelect={onUndo}
          />
          <DropdownItem
            label="Redo"
            shortcut="Ctrl+Y"
            disabled={!canRedo}
            onSelect={onRedo}
          />
          <DropdownDivider />
          <DropdownItem
            label="Find"
            shortcut="Ctrl+F"
            disabled={!folderOpen}
            onSelect={onFind}
          />
        </>
      ),
    },
    {
      key: "selection",
      label: "Selection",
      items: (
        <DropdownItem
          label="Select All"
          shortcut="Ctrl+A"
          disabled={!folderOpen}
          onSelect={onSelectAll}
        />
      ),
    },
    {
      key: "view",
      label: "View",
      items: (
        <>
          <DropdownItem
            label="Explorer"
            shortcut="Ctrl+B"
            checked={explorerOpen}
            onSelect={onToggleExplorer}
          />
          <DropdownItem
            label="Terminal Panel"
            shortcut="Ctrl+`"
            checked={panelOpen}
            onSelect={onTogglePanel}
          />
          <DropdownDivider />
          <DropdownItem
            label="Command Palette"
            shortcut="Ctrl+K"
            onSelect={onOpenCmdk}
          />
        </>
      ),
    },
    {
      key: "go",
      label: "Go",
      items: (
        <DropdownItem
          label="Go to File…"
          shortcut="Ctrl+K"
          disabled={!folderOpen}
          onSelect={onGoToFile}
        />
      ),
    },
    {
      key: "run",
      label: "Run",
      items: (
        <DropdownItem label="No run configurations" disabled />
      ),
    },
    {
      key: "terminal",
      label: "Terminal",
      items: (
        <DropdownItem
          label="New Terminal"
          shortcut="Ctrl+Shift+`"
          disabled={!folderOpen}
          onSelect={onNewTerminal}
        />
      ),
    },
    {
      key: "help",
      label: "Help",
      items: <DropdownItem label="About Harmonia" />,
    },
  ];

  return (
    <div
      ref={barRef}
      className="relative z-30 flex h-[34px] shrink-0 items-center justify-between border-b border-[var(--ide-border)] bg-[var(--ide-bg-menu)] px-2.5"
    >
      <div className="flex items-center gap-0.5">
        <div className="mr-1.5 flex items-center gap-1.5 border-r border-[var(--ide-border)] pr-2.5">
          <OrbMark size={16} />
          <span className="text-[12.5px] font-medium text-[var(--ide-text-secondary)]">
            Harmonia
          </span>
        </div>
        {menus.map((menu) => (
          <div key={menu.key} className="relative">
            <div
              role="button"
              onClick={(e) => {
                e.stopPropagation();
                setOpenMenu((cur) => (cur === menu.key ? null : menu.key));
              }}
              className={`cursor-pointer rounded-md px-2.5 py-[5px] text-[12.5px] transition-colors ${
                openMenu === menu.key
                  ? "bg-[var(--ide-surface-2)] text-[var(--ide-text)]"
                  : "text-[var(--ide-text-secondary)] hover:bg-[var(--ide-surface-2)] hover:text-[var(--ide-text)]"
              }`}
            >
              {menu.label}
            </div>
            {openMenu === menu.key && (
              <div
                role="menu"
                className="absolute top-full left-0 z-40 mt-1.5 flex min-w-[250px] flex-col gap-[1px] rounded-lg border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] p-[5px] shadow-[0_12px_32px_rgba(0,0,0,.55)]"
                onClick={() => setOpenMenu(null)}
              >
                {menu.items}
              </div>
            )}
          </div>
        ))}
      </div>

      <div className="flex items-center gap-2.5">
        <button
          type="button"
          onClick={onOpenCmdk}
          className="flex items-center gap-1.5 rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface)] px-2.5 py-[5px] text-[11.5px] text-[var(--ide-text-muted)] hover:text-[var(--ide-text-secondary)]"
        >
          <kbd className="rounded bg-[var(--ide-surface-2)] px-[5px] py-px font-[family-name:var(--login-font-mono)] text-[10.5px]">
            Ctrl
          </kbd>
          <kbd className="rounded bg-[var(--ide-surface-2)] px-[5px] py-px font-[family-name:var(--login-font-mono)] text-[10.5px]">
            K
          </kbd>
          Command palette
        </button>

        {/* Presence cluster — real participants, not decorative. The
            agent's own product logo stays in its neutral, native color
            always; the glow ring (via .presence-avatar.active below) is
            the only signal for "currently active," so the two never
            collide (design overhaul point 7). */}
        <div className="flex items-center">
          <Tooltip label={humanInitials} side="bottom" align="end">
            <div className="relative z-10 flex h-6 w-6 items-center justify-center rounded-full border-2 border-[var(--ide-bg-menu)] bg-[var(--ide-surface-2)] text-[9.5px] font-semibold text-[var(--ide-text-secondary)] outline outline-1 outline-[var(--ide-border-strong)]">
              {humanInitials}
            </div>
          </Tooltip>
          {agents.map((agent, i) => (
            <Tooltip
              key={agent.id}
              label={
                agent.active
                  ? `${agent.name} — active, click to follow`
                  : agent.name
              }
              side="bottom"
              align="end"
            >
              <button
                type="button"
                disabled={!agent.active}
                onClick={() => agent.active && onToggleFollow(agent.id)}
                style={{ marginLeft: -7, zIndex: 10 - (i + 1) }}
                className={`relative flex h-6 w-6 items-center justify-center rounded-full border-2 border-[var(--ide-bg-menu)] bg-[var(--ide-surface-2)] outline outline-1 ${
                  agent.active
                    ? "cursor-pointer outline-[var(--login-accent)] shadow-[0_0_8px_rgba(76,211,194,.28)]"
                    : "cursor-default outline-[var(--ide-border-strong)]"
                } ${followingAgentId === agent.id ? "ring-2 ring-[var(--login-accent)] ring-offset-1 ring-offset-[var(--ide-bg-menu)]" : ""}`}
              >
                <AgentAvatarGlyph
                  provider={agent.provider}
                  name={agent.name}
                  size={13}
                />
              </button>
            </Tooltip>
          ))}
          {roomId && (
            // Real fix for a real gap: with the app's own Rooms sidebar
            // gone from this page (point 1), the room header's own "+
            // Add agent" — previously the only place to do this — isn't
            // reachable from the IDE at all anymore. Same real component,
            // same real POST, not a second mechanism.
            <div style={{ marginLeft: -7, zIndex: 0 }}>
              <AddAgentMenu roomId={roomId} onAdded={onAgentAdded} />
            </div>
          )}
        </div>

        {/* Follow mode's own visible indicator — never silent state; a
            human following an agent always has a clear way to see it and
            stop (design overhaul point 8). */}
        {followingAgentId && (
          <div className="flex items-center gap-1.5 rounded-full border border-[rgba(76,211,194,.35)] bg-[rgba(76,211,194,.1)] py-[3px] pr-1.5 pl-2 text-[11px] text-[var(--login-accent)]">
            <svg
              width="11"
              height="11"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            >
              <path d="M1 8s2.5-5 7-5 7 5 7 5-2.5 5-7 5-7-5-7-5z" />
              <circle cx="8" cy="8" r="2" />
            </svg>
            Following{" "}
            {agents.find((a) => a.id === followingAgentId)?.name ?? "agent"}
            <Tooltip label="Stop following" side="bottom" align="end">
              <button
                type="button"
                onClick={onStopFollowing}
                className="flex items-center opacity-70 hover:opacity-100"
              >
                <svg
                  width="10"
                  height="10"
                  viewBox="0 0 16 16"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.6"
                >
                  <path d="M4 4l8 8M12 4l-8 8" />
                </svg>
              </button>
            </Tooltip>
          </div>
        )}
      </div>
    </div>
  );
}
