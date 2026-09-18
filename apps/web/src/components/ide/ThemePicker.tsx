"use client";

import { useEffect, useRef, useState } from "react";
import { EDITOR_THEMES } from "@/lib/editorThemes";

function PaletteIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
    >
      <path d="M8 1.5a6.5 6.5 0 100 13c.9 0 1.5-.6 1.5-1.3 0-.35-.15-.65-.35-.9-.2-.25-.35-.55-.35-.9 0-.7.6-1.3 1.3-1.3H11a3 3 0 003-3c0-3.35-2.7-5.6-6-5.6z" />
      <circle cx="4.8" cy="7.2" r=".9" fill="currentColor" stroke="none" />
      <circle cx="6.6" cy="4.3" r=".9" fill="currentColor" stroke="none" />
      <circle cx="9.8" cy="4.3" r=".9" fill="currentColor" stroke="none" />
      <circle cx="11.4" cy="7.2" r=".9" fill="currentColor" stroke="none" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <path d="M3.5 8.5l3 3 6-7" />
    </svg>
  );
}

/**
 * The IDE's own real, designed editor-theme picker — a genuine floating
 * menu matching this codebase's own established dropdown pattern
 * (AddAgentMenu's own trigger + floating panel), replacing a bare native
 * <select> that carried no icon, no swatch, and no visual match to the
 * rest of the IDE's chrome. Opens upward (side="top") since its trigger
 * lives in the IDE's own bottom status bar — there's no room below it.
 * Closes on a real outside "click" (not "mousedown"), the same pattern
 * AddAgentMenu already uses correctly: a document-level "click" listener
 * fires after React's own delegated handlers, so it can never race and
 * eat a menu item's own click the way a "mousedown" listener would.
 */
export function ThemePicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const active = EDITOR_THEMES.find((t) => t.id === value) ?? EDITOR_THEMES[0];

  useEffect(() => {
    if (!open) return;
    function onDocumentClick(event: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
  }, [open]);

  const builtin = EDITOR_THEMES.filter((t) => t.builtin);
  const community = EDITOR_THEMES.filter((t) => !t.builtin);

  return (
    <div ref={menuRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex cursor-pointer items-center gap-1.5 rounded px-1.5 py-0.5 text-[11px] font-medium text-[var(--ide-bg)] hover:bg-[rgba(10,12,17,.12)]"
      >
        <PaletteIcon />
        {active.label}
      </button>

      {open && (
        <div className="absolute bottom-[calc(100%+6px)] right-0 z-50 w-[230px] rounded-[10px] border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] p-1.5 text-[var(--ide-text)] shadow-[0_8px_24px_rgba(0,0,0,0.5)]">
          <div className="px-2 pt-1 pb-1 font-[family-name:var(--login-font-mono)] text-[10.5px] tracking-wide text-[var(--ide-text-muted)] uppercase">
            Built-in
          </div>
          {builtin.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => {
                onChange(t.id);
                setOpen(false);
              }}
              className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-[12.5px] hover:bg-[var(--ide-surface)]"
            >
              <span
                className="h-3.5 w-3.5 shrink-0 rounded-full border border-[var(--ide-border-strong)]"
                style={{ background: t.swatch }}
              />
              <span className="flex-1 truncate">{t.label}</span>
              {t.id === value && <CheckIcon />}
            </button>
          ))}
          <div className="my-1 h-px bg-[var(--ide-border)]" />
          <div className="px-2 pt-1 pb-1 font-[family-name:var(--login-font-mono)] text-[10.5px] tracking-wide text-[var(--ide-text-muted)] uppercase">
            Community
          </div>
          {community.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => {
                onChange(t.id);
                setOpen(false);
              }}
              className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-[12.5px] hover:bg-[var(--ide-surface)]"
            >
              <span
                className="h-3.5 w-3.5 shrink-0 rounded-full border border-[var(--ide-border-strong)]"
                style={{ background: t.swatch }}
              />
              <span className="flex-1 truncate">{t.label}</span>
              {t.id === value && <CheckIcon />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
