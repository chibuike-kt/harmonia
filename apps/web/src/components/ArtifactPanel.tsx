"use client";

import { useEffect, useMemo, useState } from "react";
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
import { useIsMobile } from "@/lib/useIsMobile";
import { Tooltip } from "./Tooltip";

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
  const isMobile = useIsMobile();
  // Mobile is always effectively "enlarged" — full screen, replacing the
  // timeline rather than sharing width with it — so every place enlarged
  // alone used to gate resize/width behavior below also checks this.
  const fullScreen = enlarged || isMobile;

  // Drives the fade/slide-in below. A fresh ArtifactPanelContent instance
  // mounts every time a genuinely different artifact opens (see
  // ArtifactPanel's own key, further down) — the plain empty-deps effect
  // this is correct here, unlike a persistently-mounted panel that only
  // toggles an internal open flag (see RoomInfoPanel, which needs the
  // extra open-driven reset this doesn't).
  const [entered, setEntered] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setEntered(true));
    return () => cancelAnimationFrame(id);
  }, []);

  const handleResizeStart = (event: React.MouseEvent) => {
    event.preventDefault();
    if (fullScreen) return;
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
    // artifact.label is already a complete filename with its own
    // extension (lib/messageContent's suggestedName, real or a
    // language-plus-sequence fallback) — appending one here on top of it
    // used to produce "chatgpt-snippet.go.go"-style double extensions
    // before that was true. Sanitized, not re-extended.
    const blob = new Blob([artifact.code], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = artifact.label.replace(/[^\w.-]+/g, "-");
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <div
      className={
        fullScreen
          ? "fixed inset-0 z-[60] h-screen w-screen"
          : // Detached, floating card — a gap on every side (m-3), rather
            // than a flush divider panel sharing the timeline's own
            // edges. This outer layer only handles position/margin/the
            // resize handle; rounding and clipping live one level in
            // (below) so the handle, anchored just outside that layer's
            // own left edge, isn't clipped by its own overflow-hidden.
            // h-[calc(100%-1.5rem)] is h-full minus m-3's 0.75rem
            // top/bottom margins — h-screen plus a vertical margin would
            // overflow past 100vh.
            "relative m-3 h-[calc(100%-1.5rem)] shrink-0"
      }
      style={
        fullScreen
          ? undefined
          : { width, transition: resizing ? "none" : "width 0.18s ease" }
      }
    >
      {!fullScreen && (
        <div className="absolute -left-[3px] top-0 z-10 h-full">
          <Tooltip label="Resize" className="h-full">
            <div
              onMouseDown={handleResizeStart}
              className="h-full w-1.5 cursor-col-resize hover:bg-[var(--login-accent)]/35"
            />
          </Tooltip>
        </div>
      )}
      <div
        className={
          // Fades in either way; slides up from the bottom full screen
          // (the natural direction for a sheet replacing the whole
          // view), in from the right as a side panel (the direction
          // it's anchored to) — both settle to their resting position
          // via the same transition/duration, just a different axis.
          (fullScreen
            ? "flex h-full w-full flex-col border-l border-[var(--login-border)] bg-[var(--login-surface)] "
            : "flex h-full flex-col overflow-hidden rounded-2xl border border-[var(--login-border)] bg-[var(--login-surface)] shadow-[0_12px_40px_rgba(0,0,0,0.45)] ") +
          "transition-[opacity,transform] duration-200 ease-out " +
          (entered
            ? "translate-x-0 translate-y-0 opacity-100"
            : fullScreen
              ? "translate-y-3 opacity-0"
              : "translate-x-3 opacity-0")
        }
      >
        <div className="flex shrink-0 items-center justify-between border-b border-[var(--login-border)] px-3.5 py-3">
          <span className="truncate font-[family-name:var(--login-font-mono)] text-[13px] text-[var(--login-text-secondary)]">
            {artifact.label}
          </span>
          <div className="flex items-center gap-0.5">
            <Tooltip label={copied ? "Copied!" : "Copy"}>
              <button
                type="button"
                onClick={() => void handleCopy()}
                className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
              >
                <CopyIcon />
              </button>
            </Tooltip>
            <Tooltip label="Download">
              <button
                type="button"
                onClick={handleDownload}
                className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
              >
                <DownloadIcon />
              </button>
            </Tooltip>
            {/* Meaningless on mobile — the panel is always full screen
              there regardless of `enlarged`, so toggling it would be a
              button with no visible effect. */}
            {!isMobile && (
              <Tooltip label={enlarged ? "Shrink" : "Enlarge"}>
                <button
                  type="button"
                  onClick={() => setEnlarged((v) => !v)}
                  className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
                >
                  <EnlargeIcon />
                </button>
              </Tooltip>
            )}
            <Tooltip label="Close">
              <button
                type="button"
                onClick={onClose}
                className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
              >
                <CloseIcon />
              </button>
            </Tooltip>
          </div>
        </div>
        <pre className="hljs no-scrollbar m-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-words p-4 font-[family-name:var(--login-font-mono)] text-[12.5px] leading-[1.6]">
          <code dangerouslySetInnerHTML={{ __html: highlighted }} />
        </pre>
      </div>
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
