// Real file-extension icons — sourced directly from material-icon-theme
// (github.com/material-extensions/vscode-material-icon-theme, MIT
// licensed), the same "safe, licensed, already-correct" sourcing this
// project already uses for provider logos (see providerLogos.tsx's own
// doc comment). Not hand-drawn, not a coarse 4-color bucket: this is
// their real, current icon set and their real, current
// extension/filename mapping data (material-icons.json), imported
// verbatim — apps/web/public/file-icons/ holds their actual SVGs,
// copied as-is from node_modules/material-icon-theme/icons/.
//
// The resolution algorithm below mirrors the theme's own documented
// precedence (exact filename first, since that's the only way
// "package.json", ".gitignore", or "Dockerfile" ever get their real,
// specific icon rather than a generic language one; then longest-
// matching compound extension, e.g. "d.ts" before "ts"; then a plain
// default) — real logic operating on their real data, not reinvented
// from memory.
import materialIcons from "material-icon-theme/dist/material-icons.json";

interface IconDefinitions {
  [name: string]: { iconPath: string };
}

const icons = materialIcons as unknown as {
  iconDefinitions: IconDefinitions;
  fileExtensions: Record<string, string>;
  fileNames: Record<string, string>;
  folderNames: Record<string, string>;
  folderNamesExpanded: Record<string, string>;
  file: string;
  folder: string;
  folderExpanded: string;
};

function iconUrl(defName: string | undefined, fallback: string): string {
  const name = defName && icons.iconDefinitions[defName] ? defName : fallback;
  return `/file-icons/${name}.svg`;
}

/** Real icon URL for a file, resolved by its real name — exact
 *  filename match (package.json, .gitignore, Dockerfile) takes
 *  precedence over extension, then the longest matching compound
 *  extension (d.ts before ts), then the theme's own generic file icon. */
export function fileIconUrl(name: string): string {
  const lower = name.toLowerCase();
  if (icons.fileNames[lower]) {
    return iconUrl(icons.fileNames[lower], icons.file);
  }
  const parts = lower.split(".");
  // Try every compound suffix from longest to shortest: "foo.spec.ts" ->
  // "spec.ts" -> "ts" — a compound entry (e.g. "d.ts") must win over the
  // plain "ts" one it's more specific than.
  for (let i = 1; i < parts.length; i++) {
    const suffix = parts.slice(i).join(".");
    if (icons.fileExtensions[suffix]) {
      return iconUrl(icons.fileExtensions[suffix], icons.file);
    }
  }
  return iconUrl(undefined, icons.file);
}

/** Real icon URL for a folder, by its real name — real theme-specific
 *  folders (e.g. "src", "node_modules", ".git") get their own icon,
 *  same exact-match-first precedence as files. */
export function folderIconUrl(name: string, expanded: boolean): string {
  const lower = name.toLowerCase();
  const map = expanded ? icons.folderNamesExpanded : icons.folderNames;
  const fallback = expanded ? icons.folderExpanded : icons.folder;
  return iconUrl(map[lower], fallback);
}
