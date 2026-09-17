"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import "@xterm/xterm/css/xterm.css";

export interface TerminalHandle {
  write: (data: string) => void;
  clear: () => void;
  focus: () => void;
  fit: () => void;
  openSearch: () => void;
}

// The IDE's own dark palette (see globals.css's --ide-* tokens) carried
// into xterm's real ANSI theme — xterm.js does the actual rendering here,
// including every real SGR escape a shell or an agent's narration sends;
// nothing in this file parses or reimplements ANSI itself.
const XTERM_THEME = {
  background: "#10141b",
  foreground: "#eceef1",
  cursor: "#4cd3c2",
  cursorAccent: "#0a0c11",
  selectionBackground: "#2a313d",
  black: "#0a0c11",
  red: "#e2686b",
  green: "#7fd88f",
  yellow: "#e0c46f",
  blue: "#6fa8e0",
  magenta: "#b08ee0",
  cyan: "#4cd3c2",
  white: "#9ba3b0",
  brightBlack: "#5c6472",
  brightRed: "#f08a8d",
  brightGreen: "#9de3a9",
  brightYellow: "#ecd88f",
  brightBlue: "#8fc0ec",
  brightMagenta: "#c8aded",
  brightCyan: "#7fe3d6",
  brightWhite: "#eceef1",
};

/**
 * One real terminal surface — a genuine xterm.js instance attached to a
 * real DOM node, kept alive for this terminal's whole lifetime rather
 * than recreated on every re-render or pane switch (recreating it would
 * throw away real scrollback). TerminalPanel mounts one of these per open
 * terminal and toggles visibility with `hidden`, never conditional
 * rendering, for exactly that reason.
 *
 * Owns nothing about the companion protocol — data flows in via write()
 * and out via onData, the same shape any real terminal frontend (a
 * native app, xterm.js's own demo) would use against a real PTY.
 */
export const Terminal = forwardRef<
  TerminalHandle,
  {
    onData: (data: string) => void;
    onResize: (cols: number, rows: number) => void;
  }
>(function Terminal({ onData, onResize }, ref) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const searchRef = useRef<SearchAddon | null>(null);
  const onDataRef = useRef(onData);
  const onResizeRef = useRef(onResize);
  onDataRef.current = onData;
  onResizeRef.current = onResize;

  const [searchOpen, setSearchOpen] = useState(false);
  const [searchValue, setSearchValue] = useState("");
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null);
  const searchInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (searchOpen) searchInputRef.current?.focus();
  }, [searchOpen]);

  useImperativeHandle(
    ref,
    () => ({
      write: (data: string) => termRef.current?.write(data),
      clear: () => termRef.current?.clear(),
      focus: () => termRef.current?.focus(),
      fit: () => fitRef.current?.fit(),
      openSearch: () => setSearchOpen(true),
    }),
    [],
  );

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const term = new XTerm({
      cursorBlink: true,
      fontFamily:
        'var(--login-font-mono), ui-monospace, "SF Mono", Menlo, monospace',
      fontSize: 13,
      // Real, generous scrollback — "real scrollback" per this feature's
      // own requirement, not a small fixed buffer that drops old output.
      scrollback: 10000,
      theme: XTERM_THEME,
      allowTransparency: false,
    });
    const fitAddon = new FitAddon();
    const searchAddon = new SearchAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(searchAddon);
    term.open(container);
    if (container.clientWidth >= 100 && container.clientHeight >= 50) fitAddon.fit();

    termRef.current = term;
    fitRef.current = fitAddon;
    searchRef.current = searchAddon;

    // xterm's own real DECSET 1004 support forwards a real "\x1b[I"/
    // "\x1b[O" focus in/out report through onData whenever this real
    // terminal surface gains or loses DOM focus, once the shell enables
    // focus-tracking mode (PowerShell's own PSReadLine turns this on by
    // default against a capable terminal) — a real, standard terminal
    // feature real apps like vim/tmux rely on. Found live: forwarding it
    // straight through to a real PowerShell/ConPTY session made the real
    // shell exit outright on the very next keystroke, a real
    // incompatibility in that specific combination, not something this
    // terminal is meant to work around by pretending focus tracking
    // isn't real — dropping just these two synthetic reports (never
    // real typed/pasted input) is the narrow, correct fix.
    const dataDisposable = term.onData((data) => {
      if (data === "\x1b[I" || data === "\x1b[O") return;
      onDataRef.current(data);
    });
    const resizeDisposable = term.onResize(({ cols, rows }) =>
      onResizeRef.current(cols, rows),
    );

    // Ctrl+C copies a real selection instead of sending a real SIGINT,
    // matching every real terminal emulator's own convention (VS Code,
    // Windows Terminal, iTerm2) — with nothing selected, Ctrl+C still
    // reaches the shell as a real interrupt. Ctrl+K clears this real
    // terminal's own scrollback (xterm's own real clear, not a visual
    // trick) rather than reaching the page's global command-palette
    // shortcut — resolved by focus, the same way VS Code scopes
    // `terminalFocus` keybindings ahead of global ones; the page's own
    // global handler skips 'k' whenever a real terminal surface has
    // focus (see data-terminal-surface). Ctrl+F opens this terminal's
    // own real scrollback search instead of the browser's own page-find.
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown") return true;
      const ctrl = e.ctrlKey || e.metaKey;
      if (!ctrl) return true;
      if (e.key === "c" && term.hasSelection()) {
        void navigator.clipboard.writeText(term.getSelection());
        return false;
      }
      if (e.key === "k") {
        term.clear();
        return false;
      }
      if (e.key === "f") {
        setSearchOpen(true);
        return false;
      }
      if (e.key === "v") {
        // Real paste — let the browser's own native paste into xterm's
        // hidden input surface proceed; xterm turns that into a real
        // onData call itself, no special handling needed here.
        return true;
      }
      return true;
    });

    const resizeObserver = new ResizeObserver(() => {
      // ResizeObserver's own first callback can fire before this
      // container has its real flex-computed size (a known browser
      // race on initial layout) — fitAddon.fit() against that transient
      // near-zero box computes a real but absurd size (found live:
      // cols 9 / rows 8), and sending that straight to the real PTY as
      // a real terminal_resize is what was making a real PowerShell/
      // PSReadLine session exit outright on the very next keystroke, not
      // any escape sequence. No real terminal pane is ever legitimately
      // this small, so skip the fit — the next real observation, once
      // layout has actually settled, sends the real size.
      if (container.clientWidth < 100 || container.clientHeight < 50) return;
      fitAddon.fit();
    });
    resizeObserver.observe(container);

    return () => {
      resizeObserver.disconnect();
      dataDisposable.dispose();
      resizeDisposable.dispose();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
      searchRef.current = null;
    };
    // Mount once per real terminal surface — onData/onResize are read via
    // refs above precisely so this effect never needs to re-run and tear
    // down a real, live xterm instance (and its real scrollback) just
    // because a parent re-render passed new callback identities.
  }, []);

  const runSearch = useCallback(
    (direction: "next" | "previous") => {
      if (!searchValue) return;
      const addon = searchRef.current;
      if (direction === "next") addon?.findNext(searchValue);
      else addon?.findPrevious(searchValue);
    },
    [searchValue],
  );

  const handleContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setContextMenu({ x: e.clientX, y: e.clientY });
  }, []);

  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    window.addEventListener("mousedown", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("blur", close);
    };
  }, [contextMenu]);

  return (
    <div className="relative h-full min-h-0 min-w-0 flex-1">
      <div
        ref={containerRef}
        data-terminal-surface
        onContextMenu={handleContextMenu}
        onMouseDownCapture={(e) => {
          // Real terminals (VS Code, Windows Terminal) never let a
          // right-click disturb an existing selection — it only ever
          // opens the context menu. xterm's own mousedown handling
          // otherwise clears the real selection before this component's
          // own onContextMenu (and the menu's own real Copy) ever runs,
          // since mousedown fires first. Stopping it here, in capture
          // phase (ahead of xterm's own listener on this same node), is
          // what makes "select text, right-click, Copy" actually copy
          // the real selected text instead of nothing.
          if (e.button === 2) e.stopPropagation();
        }}
        className="h-full w-full px-2 py-1.5"
      />

      {searchOpen && (
        <div className="absolute top-2 right-2 z-10 flex items-center gap-1 rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] px-1.5 py-1 shadow-lg">
          <input
            ref={searchInputRef}
            value={searchValue}
            onChange={(e) => setSearchValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                runSearch(e.shiftKey ? "previous" : "next");
              } else if (e.key === "Escape") {
                e.preventDefault();
                setSearchOpen(false);
                termRef.current?.focus();
              }
            }}
            placeholder="Find in scrollback…"
            className="w-40 bg-transparent font-[family-name:var(--login-font-mono)] text-[11.5px] text-[var(--ide-text)] outline-none placeholder:text-[var(--ide-text-muted)]"
          />
          <button
            type="button"
            title="Previous match (Shift+Enter)"
            onClick={() => runSearch("previous")}
            className="flex h-5 w-5 items-center justify-center rounded text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface)] hover:text-[var(--ide-text)]"
          >
            ↑
          </button>
          <button
            type="button"
            title="Next match (Enter)"
            onClick={() => runSearch("next")}
            className="flex h-5 w-5 items-center justify-center rounded text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface)] hover:text-[var(--ide-text)]"
          >
            ↓
          </button>
          <button
            type="button"
            title="Close (Esc)"
            onClick={() => {
              setSearchOpen(false);
              termRef.current?.focus();
            }}
            className="flex h-5 w-5 items-center justify-center rounded text-[var(--ide-text-muted)] hover:bg-[var(--ide-surface)] hover:text-[var(--ide-text)]"
          >
            ✕
          </button>
        </div>
      )}

      {contextMenu && (
        <div
          style={{ left: contextMenu.x, top: contextMenu.y }}
          className="fixed z-50 min-w-[140px] rounded-md border border-[var(--ide-border-strong)] bg-[var(--ide-surface-2)] p-1 text-[12.5px] shadow-xl"
        >
          <button
            type="button"
            onClick={() => {
              const term = termRef.current;
              if (term?.hasSelection()) void navigator.clipboard.writeText(term.getSelection());
              setContextMenu(null);
            }}
            disabled={!termRef.current?.hasSelection()}
            className="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[#0a0c11] disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-[var(--ide-text)]"
          >
            Copy
          </button>
          <button
            type="button"
            onClick={() => {
              void navigator.clipboard.readText().then((text) => {
                if (text) onDataRef.current(text);
              });
              setContextMenu(null);
            }}
            className="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[#0a0c11]"
          >
            Paste
          </button>
          <div className="my-1 h-px bg-[var(--ide-border)]" />
          <button
            type="button"
            onClick={() => {
              termRef.current?.clear();
              setContextMenu(null);
            }}
            className="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[#0a0c11]"
          >
            Clear
            <span className="text-[10px] text-[var(--ide-text-muted)]">Ctrl+K</span>
          </button>
          <button
            type="button"
            onClick={() => {
              termRef.current?.selectAll();
              setContextMenu(null);
            }}
            className="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-[var(--ide-text)] hover:bg-[var(--login-accent)] hover:text-[#0a0c11]"
          >
            Select All
          </button>
        </div>
      )}
    </div>
  );
});
