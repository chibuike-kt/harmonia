"use client";

import { useMemo, useState } from "react";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import css from "highlight.js/lib/languages/css";
import go from "highlight.js/lib/languages/go";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";
import { CloseIcon, CopyIcon, DownloadIcon, EnlargeIcon } from "./icons";

// A fixed, common subset registered up front rather than highlight.js's
// full ~190-language bundle — real tokenization (accurate keyword/
// string/comment spans, not a hand-rolled regex) for the languages an
// agent's code block is actually likely to be in, without the bundle
// size of languages this app has no use for.
let registered = false;
function ensureLanguagesRegistered() {
  if (registered) return;
  hljs.registerLanguage("javascript", javascript);
  hljs.registerLanguage("typescript", typescript);
  hljs.registerLanguage("python", python);
  hljs.registerLanguage("go", go);
  hljs.registerLanguage("json", json);
  hljs.registerLanguage("bash", bash);
  hljs.registerLanguage("sh", bash);
  hljs.registerLanguage("css", css);
  hljs.registerLanguage("xml", xml);
  hljs.registerLanguage("html", xml);
  hljs.registerLanguage("sql", sql);
  hljs.registerLanguage("yaml", yaml);
  hljs.registerLanguage("yml", yaml);
  hljs.registerLanguage("markdown", markdown);
  registered = true;
}

export interface ArtifactContent {
  label: string;
  language: string;
  code: string;
  /** "text" skips syntax highlighting entirely — hljs.highlightAuto
   *  guessing a "language" for plain pasted prose produces wrong,
   *  distracting tokenization, not a helpful rendering. */
  kind: "code" | "text";
}

interface ArtifactPanelProps {
  artifact: ArtifactContent | null;
  onClose: () => void;
}

// File extension per language — good enough for a download filename;
// there's no real filename in a message, just a fenced code block's
// language tag (see messageContent.ts's own comment on why: no new
// storage, this is re-derived from the message every time).
const EXTENSIONS: Record<string, string> = {
  javascript: "js",
  typescript: "ts",
  python: "py",
  go: "go",
  json: "json",
  bash: "sh",
  sh: "sh",
  css: "css",
  html: "html",
  xml: "xml",
  sql: "sql",
  yaml: "yml",
  yml: "yml",
  markdown: "md",
};

const HTML_ESCAPES: Record<string, string> = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
};

function escapeHtml(text: string): string {
  return text.replace(/[&<>]/g, (c) => HTML_ESCAPES[c]);
}

function highlight(artifact: ArtifactContent): string {
  if (artifact.kind === "text") {
    return escapeHtml(artifact.code);
  }
  ensureLanguagesRegistered();
  if (artifact.language !== "text" && hljs.getLanguage(artifact.language)) {
    return hljs.highlight(artifact.code, { language: artifact.language }).value;
  }
  return hljs.highlightAuto(artifact.code).value;
}

interface ArtifactPanelContentProps {
  artifact: ArtifactContent;
  onClose: () => void;
}

// Draggable-width bounds for the normal (non-enlarged) panel — same
// drag-from-the-border resize Sidebar.tsx already has, ported here:
// "expand" means grabbing the panel's own left edge, not just a
// fullscreen-toggle button.
const MIN_WIDTH = 320;
const MAX_WIDTH = 960;
const DEFAULT_WIDTH = 420;

// Split from ArtifactPanel and keyed by the outer component on the
// artifact's identity: mounting a fresh instance per opened artifact is
// what resets `enlarged`/`copied`/`width` for a newly opened one, without
// an effect synchronizing local state off a prop change.
function ArtifactPanelContent({
  artifact,
  onClose,
}: ArtifactPanelContentProps) {
  const [enlarged, setEnlarged] = useState(false);
  const [copied, setCopied] = useState(false);
  const [width, setWidth] = useState(DEFAULT_WIDTH);
  const [resizing, setResizing] = useState(false);

  const handleResizeStart = (event: React.MouseEvent) => {
    event.preventDefault();
    if (enlarged) return;
    setResizing(true);
    const onMove = (moveEvent: MouseEvent) => {
      // The panel is anchored to the right edge of the screen, so its
      // width is the distance from the drag point to that edge, not the
      // drag point itself (Sidebar's own handle, anchored to the left
      // edge, can use clientX directly — this is the mirror image of
      // that).
      setWidth(
        Math.min(
          MAX_WIDTH,
          Math.max(MIN_WIDTH, window.innerWidth - moveEvent.clientX),
        ),
      );
    };
    const onUp = () => {
      setResizing(false);
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
  };

  const highlighted = useMemo(() => highlight(artifact), [artifact]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(artifact.code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard access can fail (permissions, insecure context) —
      // there's nothing more useful to do than leave the button inert.
    }
  };

  const handleDownload = () => {
    const ext = EXTENSIONS[artifact.language] ?? "txt";
    const blob = new Blob([artifact.code], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${artifact.label.replace(/[^\w.-]+/g, "-")}.${ext}`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <div
      className={
        enlarged
          ? "fixed inset-0 z-[60] flex h-screen w-screen flex-col border-l border-[var(--login-border)] bg-[var(--login-surface)]"
          : "relative flex h-screen shrink-0 flex-col border-l border-[var(--login-border)] bg-[var(--login-surface)]"
      }
      style={
        enlarged
          ? undefined
          : { width, transition: resizing ? "none" : "width 0.18s ease" }
      }
    >
      {!enlarged && (
        <div
          onMouseDown={handleResizeStart}
          title="Resize"
          className="absolute -left-[3px] top-0 z-10 h-full w-1.5 cursor-col-resize hover:bg-[var(--login-accent)]/35"
        />
      )}
      <div className="flex shrink-0 items-center justify-between border-b border-[var(--login-border)] px-3.5 py-3">
        <span className="truncate font-[family-name:var(--login-font-mono)] text-[13px] text-[var(--login-text-secondary)]">
          {artifact.label}
        </span>
        <div className="flex items-center gap-0.5">
          <button
            type="button"
            title={copied ? "Copied" : "Copy"}
            onClick={() => void handleCopy()}
            className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
          >
            <CopyIcon />
          </button>
          <button
            type="button"
            title="Download"
            onClick={handleDownload}
            className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
          >
            <DownloadIcon />
          </button>
          <button
            type="button"
            title="Enlarge"
            onClick={() => setEnlarged((v) => !v)}
            className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
          >
            <EnlargeIcon />
          </button>
          <button
            type="button"
            title="Close"
            onClick={onClose}
            className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
          >
            <CloseIcon />
          </button>
        </div>
      </div>
      <pre className="hljs no-scrollbar m-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-words p-4 font-[family-name:var(--login-font-mono)] text-[12.5px] leading-[1.6]">
        <code dangerouslySetInnerHTML={{ __html: highlighted }} />
      </pre>
    </div>
  );
}

/**
 * Slide-out side panel for a fenced code block pulled out of an agent's
 * reply — docs/design/room-mockup.html's Artifact side panel, scoped to
 * rendering only (no editing, no versioning, no separate artifacts
 * table): re-parses and re-highlights the same message content every
 * time it opens rather than reading from any new storage.
 */
export function ArtifactPanel({ artifact, onClose }: ArtifactPanelProps) {
  if (!artifact) {
    return (
      <div className="h-screen w-0 shrink-0 overflow-hidden transition-[width] duration-150" />
    );
  }
  return (
    <ArtifactPanelContent
      key={artifact.label + artifact.code.length}
      artifact={artifact}
      onClose={onClose}
    />
  );
}
