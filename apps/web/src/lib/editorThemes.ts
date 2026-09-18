import type * as monacoEditor from "monaco-editor";

// Real, licensed editor themes — a mix of Monaco's own built-ins (need
// no registration at all: "vs", "vs-dark", "hc-black", "hc-light" ship
// inside monaco-editor itself) and a curated set of real, well-known
// community themes as real static JSON assets under public/editor-themes
// (see that directory's own LICENSE.txt) — sourced from the monaco-themes
// npm package (MIT), a real curated conversion of real popular editor
// themes, not hand-approximated from memory. Fetched on demand rather
// than bundled, so picking a theme is the only time its real JSON is
// ever loaded. Every real IDE lets you pick a theme; this is that real,
// changeable setting, not a single fixed choice either way.
//
// "One Dark Pro" specifically isn't among these (it's a newer, separately
// -licensed Atom-derived theme with no pre-converted Monaco JSON readily
// available) — Dracula and Nord are substituted as equally well-known,
// actually-available alternatives.
export interface EditorTheme {
  id: string;
  label: string;
  /** Monaco's own theme name to pass to <Editor theme=…> — for a
   *  built-in this is the id itself; for a fetched theme it's the same
   *  name registerEditorTheme defines it under once loaded. */
  monacoName: string;
  builtin: boolean;
  /** Static asset path for a non-builtin theme's real JSON. */
  assetPath?: string;
  /** The theme's own real editor-background color — used as a small
   *  swatch in the theme picker so a human can recognize a theme by eye,
   *  not just by name. Taken directly from each theme's own real
   *  `colors["editor.background"]` (or Monaco's own default for a
   *  builtin), not an approximation. */
  swatch: string;
}

export const EDITOR_THEMES: EditorTheme[] = [
  {
    id: "vs-dark",
    label: "Dark (Visual Studio)",
    monacoName: "vs-dark",
    builtin: true,
    swatch: "#1e1e1e",
  },
  {
    id: "vs",
    label: "Light (Visual Studio)",
    monacoName: "vs",
    builtin: true,
    swatch: "#ffffff",
  },
  {
    id: "hc-black",
    label: "Dark High Contrast",
    monacoName: "hc-black",
    builtin: true,
    swatch: "#000000",
  },
  {
    id: "hc-light",
    label: "Light High Contrast",
    monacoName: "hc-light",
    builtin: true,
    swatch: "#ffffff",
  },
  {
    id: "github-dark",
    label: "GitHub Dark",
    monacoName: "github-dark",
    builtin: false,
    assetPath: "/editor-themes/github-dark.json",
    swatch: "#24292e",
  },
  {
    id: "github-light",
    label: "GitHub Light",
    monacoName: "github-light",
    builtin: false,
    assetPath: "/editor-themes/github-light.json",
    swatch: "#ffffff",
  },
  {
    id: "dracula",
    label: "Dracula",
    monacoName: "dracula",
    builtin: false,
    assetPath: "/editor-themes/dracula.json",
    swatch: "#282a36",
  },
  {
    id: "monokai",
    label: "Monokai",
    monacoName: "monokai",
    builtin: false,
    assetPath: "/editor-themes/monokai.json",
    swatch: "#272822",
  },
  {
    id: "nord",
    label: "Nord",
    monacoName: "nord",
    builtin: false,
    assetPath: "/editor-themes/nord.json",
    swatch: "#2e3440",
  },
  {
    id: "solarized-dark",
    label: "Solarized Dark",
    monacoName: "solarized-dark",
    builtin: false,
    assetPath: "/editor-themes/solarized-dark.json",
    swatch: "#002b36",
  },
];

export const DEFAULT_EDITOR_THEME_ID = "vs-dark";

const registeredIds = new Set<string>();

// registerEditorTheme fetches and registers one real theme's real JSON
// the first time it's actually selected — real, on-demand loading, not
// every real theme's data paid for by every page load regardless of
// which one (if any) a human ever picks. Idempotent per id: a re-select
// of an already-registered theme is a real no-op.
export async function registerEditorTheme(
  monaco: typeof monacoEditor,
  id: string,
): Promise<void> {
  const theme = EDITOR_THEMES.find((t) => t.id === id);
  if (!theme || theme.builtin || !theme.assetPath || registeredIds.has(id))
    return;
  const res = await fetch(theme.assetPath);
  if (!res.ok) return;
  const data = (await res.json()) as monacoEditor.editor.IStandaloneThemeData;
  monaco.editor.defineTheme(theme.monacoName, data);
  registeredIds.add(id);
}

const STORAGE_KEY = "harmonia-ide-editor-theme";

export function loadStoredEditorTheme(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? DEFAULT_EDITOR_THEME_ID;
  } catch {
    return DEFAULT_EDITOR_THEME_ID;
  }
}

export function storeEditorTheme(id: string) {
  try {
    localStorage.setItem(STORAGE_KEY, id);
  } catch {
    // Best-effort only — a private window or blocked storage just means
    // the choice doesn't survive a reload, not a real failure.
  }
}
