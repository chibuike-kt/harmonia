"use client";

import { useEffect, useMemo, useState } from "react";
import { apiFetch } from "@/lib/api";
import {
  ArtifactPanel,
  type ArtifactContent,
} from "@/components/ArtifactPanel";
import { ArtifactsIcon, CodeBracketsIcon, FileIcon } from "@/components/icons";
import { collectRoomArtifacts } from "@/lib/messageContent";

// internal/message.go's own recencyLimit — the same cap the room page's
// own artifacts menu already accounts for (see its RECENCY_LIMIT_HINT),
// duplicated here for the same reason: no shared-constant mechanism
// across the Go/TS boundary in this codebase.
const RECENCY_LIMIT_HINT = 50;

interface RoomSummary {
  id: string;
  name: string;
}

interface RoomAgentLite {
  id: string;
  name: string;
}

interface ChatMessageLite {
  id: string;
  sender_kind: "human" | "agent";
  agent_id?: string;
  content: string;
  created_at: string;
}

// One artifact, with enough of its room and sender context to render and
// group it — re-derived from every room's message content every time
// this page loads, the same "no separate artifacts storage" approach the
// room page's own ArtifactsMenu already takes, just run across every
// room instead of one.
interface GlobalArtifact {
  id: string;
  roomId: string;
  roomName: string;
  messageId: string;
  senderKind: "human" | "agent";
  senderName: string;
  createdAt: string;
  kind: "code" | "text";
  language: string;
  lines: number;
  code: string;
}

type Tab = "all" | "yours" | "shared";

// "Yours" / "Shared with you" map onto this app's actual model (there's
// no real multi-user room sharing yet — see SettingsModal's own "Team &
// roles aren't built yet" placeholder) rather than claude.ai's literal
// meaning: a human pasted-or-sent artifact is "yours," one an agent
// produced in reply is "received," i.e. shared with you by that agent.
const TABS: { id: Tab; label: string }[] = [
  { id: "all", label: "All" },
  { id: "yours", label: "Yours" },
  { id: "shared", label: "Shared with you" },
];

function formatRelativeTime(iso: string): string {
  const diffMinutes = Math.floor(
    (Date.now() - new Date(iso).getTime()) / 60000,
  );
  if (diffMinutes < 1) return "just now";
  if (diffMinutes < 60) return `${diffMinutes}m ago`;
  const hours = Math.floor(diffMinutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function artifactLabel(a: GlobalArtifact): string {
  return a.kind === "text"
    ? `${a.senderName.toLowerCase()}-pasted-text.txt`
    : `${a.senderName.toLowerCase()}-snippet.${a.language}`;
}

function codePreview(code: string): string {
  const firstLines = code.split("\n").slice(0, 3).join(" ").trim();
  return firstLines.length > 140 ? `${firstLines.slice(0, 140)}…` : firstLines;
}

/**
 * Cross-room artifacts library — docs the user asked for by pointing at
 * claude.ai/artifacts: every file pasted, sent, or received across every
 * room, in one place, grouped the same way (All / Yours / Shared with
 * you) rather than scattered one room's ArtifactsMenu at a time. Reuses
 * that same detection (lib/messageContent's collectRoomArtifacts) and the
 * same viewer (ArtifactPanel) — no second artifact concept, just a wider
 * scope than any one room's own menu.
 */
export default function ArtifactsPage() {
  const [artifacts, setArtifacts] = useState<GlobalArtifact[] | null>(null);
  const [mayBeIncomplete, setMayBeIncomplete] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("all");
  const [openArtifact, setOpenArtifact] = useState<ArtifactContent | null>(
    null,
  );

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const rooms = await apiFetch<RoomSummary[]>("/v1/rooms");
        const perRoom = await Promise.all(
          rooms.map(async (r) => {
            const [messages, agents] = await Promise.all([
              apiFetch<ChatMessageLite[]>(`/v1/rooms/${r.id}/messages`),
              apiFetch<RoomAgentLite[]>(`/v1/rooms/${r.id}/agents`),
            ]);
            const agentNames: Record<string, string> = {};
            agents.forEach((a) => {
              agentNames[a.id] = a.name;
            });
            const byId = new Map(messages.map((m) => [m.id, m]));
            const roomArtifacts: GlobalArtifact[] = collectRoomArtifacts(
              messages,
            ).map((a) => {
              const source = byId.get(a.messageId)!;
              return {
                id: `${r.id}-${a.id}`,
                roomId: r.id,
                roomName: r.name,
                messageId: a.messageId,
                senderKind: source.sender_kind,
                senderName:
                  source.sender_kind === "human"
                    ? "You"
                    : (source.agent_id && agentNames[source.agent_id]) ||
                      "Agent",
                createdAt: source.created_at,
                kind: a.kind,
                language: a.language,
                lines: a.lines,
                code: a.code,
              };
            });
            return {
              artifacts: roomArtifacts,
              capped: messages.length >= RECENCY_LIMIT_HINT,
            };
          }),
        );
        if (cancelled) return;
        setArtifacts(perRoom.flatMap((r) => r.artifacts));
        setMayBeIncomplete(perRoom.some((r) => r.capped));
      } catch (err) {
        if (!cancelled) {
          setLoadError(
            err instanceof Error ? err.message : "Failed to load artifacts.",
          );
        }
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  const counts = useMemo(() => {
    const all = artifacts ?? [];
    return {
      all: all.length,
      yours: all.filter((a) => a.senderKind === "human").length,
      shared: all.filter((a) => a.senderKind === "agent").length,
    };
  }, [artifacts]);

  const filtered = useMemo(() => {
    const all = artifacts ?? [];
    const scoped =
      tab === "yours"
        ? all.filter((a) => a.senderKind === "human")
        : tab === "shared"
          ? all.filter((a) => a.senderKind === "agent")
          : all;
    return [...scoped].sort((a, b) =>
      a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : 0,
    );
  }, [artifacts, tab]);

  return (
    <div className="flex h-full min-w-0 flex-1">
      <main className="flex min-w-0 flex-1 flex-col">
        <div className="shrink-0 border-b border-[var(--login-border)] px-8 py-6">
          <h1 className="flex items-center gap-2.5 text-[20px] font-semibold text-[var(--login-text)]">
            <ArtifactsIcon size={20} />
            Artifacts
          </h1>
          <p className="mt-1 text-[13.5px] text-[var(--login-text-muted)]">
            Every file pasted, sent, or received across all your rooms.
          </p>
        </div>

        <div className="flex shrink-0 gap-1.5 border-b border-[var(--login-border)] px-8 py-3">
          {TABS.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => setTab(t.id)}
              className={`flex items-center gap-1.5 rounded-full border px-3.5 py-1.5 text-[13px] ${
                tab === t.id
                  ? "border-[var(--login-accent)] bg-[var(--login-accent)]/10 text-[var(--login-accent)]"
                  : "border-[var(--login-border-strong)] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
              }`}
            >
              {t.label}
              {artifacts !== null && (
                <span className="font-[family-name:var(--login-font-mono)] text-[11.5px] opacity-70">
                  {counts[t.id]}
                </span>
              )}
            </button>
          ))}
        </div>

        <div className="no-scrollbar flex-1 overflow-y-auto px-8 py-6">
          {loadError ? (
            <p className="text-[13.5px] text-[var(--room-warn)]">{loadError}</p>
          ) : artifacts === null ? (
            <p className="text-[13.5px] text-[var(--login-text-muted)]">
              Loading artifacts…
            </p>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-16 text-center">
              <ArtifactsIcon size={28} />
              <p className="text-[14px] text-[var(--login-text-secondary)]">
                {tab === "yours"
                  ? "Nothing you've pasted or sent yet."
                  : tab === "shared"
                    ? "No agent has shared a file with you yet."
                    : "No artifacts yet."}
              </p>
              <p className="max-w-[380px] text-[12.5px] text-[var(--login-text-muted)]">
                A fenced code block or a large pasted block, in any room, shows
                up here.
              </p>
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {filtered.map((a) => (
                <button
                  key={a.id}
                  type="button"
                  onClick={() =>
                    setOpenArtifact({
                      label: artifactLabel(a),
                      language: a.language,
                      code: a.code,
                      kind: a.kind,
                    })
                  }
                  className="flex flex-col items-start gap-2 rounded-xl border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] p-4 text-left hover:border-[var(--login-accent)]"
                >
                  <span className="flex w-full items-center gap-1.5 font-[family-name:var(--login-font-mono)] text-[12.5px] text-[var(--login-text)]">
                    {a.kind === "text" ? <FileIcon /> : <CodeBracketsIcon />}
                    {a.kind === "text" ? "Pasted text" : a.language} · {a.lines}{" "}
                    line{a.lines === 1 ? "" : "s"}
                  </span>
                  {codePreview(a.code) && (
                    <span className="line-clamp-2 w-full font-[family-name:var(--login-font-mono)] text-[11.5px] leading-[1.5] text-[var(--login-text-muted)]">
                      {codePreview(a.code)}
                    </span>
                  )}
                  <span className="mt-1 flex w-full items-center gap-1.5 truncate text-[11.5px] text-[var(--login-text-muted)]">
                    <span className="truncate">{a.roomName}</span>
                    <span>·</span>
                    {a.senderName}
                    <span>·</span>
                    {formatRelativeTime(a.createdAt)}
                  </span>
                </button>
              ))}
            </div>
          )}

          {mayBeIncomplete && (
            <p className="mt-6 text-[11.5px] text-[var(--login-text-muted)]">
              Reflects each room&apos;s most recent messages, not necessarily
              its full history.
            </p>
          )}
        </div>
      </main>
      <ArtifactPanel
        artifact={openArtifact}
        onClose={() => setOpenArtifact(null)}
      />
    </div>
  );
}
