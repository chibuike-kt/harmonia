"use client";

import type { ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import { useParams } from "next/navigation";
import { apiFetch, apiUrl } from "@/lib/api";
import {
  ArtifactPanel,
  type ArtifactContent,
} from "@/components/ArtifactPanel";
import { AddAgentMenu } from "@/components/AddAgentMenu";
import { Composer, type RoomAgent } from "@/components/Composer";
import {
  MessageRow,
  displaySenderName,
  type ChatMessage,
} from "@/components/MessageRow";
import { TypingIndicator } from "@/components/TypingIndicator";
import {
  RoomInfoPanel,
  type Decision,
  type RoomAgentSummary,
} from "@/components/RoomInfoPanel";
import { ChevronDownIcon, CoinIcon, InfoIcon } from "@/components/icons";
import { AgentAvatarGlyph } from "@/components/providerLogos";
import {
  estimateCostUSD,
  formatCostUSD,
  formatTokenCount,
} from "@/lib/tokenPricing";

interface AgentPresence {
  agent_id: string;
  status: string;
}

// The snapshot's historical events come from event.Store (Go), whose
// Event.Type is a string like "TASK_CREATED". Live envelopes come from
// internal/protocol.Envelope instead, whose Type is "TASK.CREATE" — a
// different format for the same operation. Both are handled below.
interface HistoricalEvent {
  id: number;
  task_id?: string;
  agent_id?: string;
  type: string;
  payload: Record<string, unknown>;
  created_at: string;
}

interface Envelope {
  id: string;
  type: string;
  timestamp: string;
  task_id?: string;
  sender: { agent_id: string };
  payload: Record<string, unknown>;
}

interface RoomUpdate {
  room_id: string;
  name: string;
}

interface RealtimeMessage {
  kind: "event" | "presence" | "message" | "room";
  event?: Envelope;
  presence?: AgentPresence;
  message?: ChatMessage;
  room?: RoomUpdate;
}

interface Snapshot {
  events: HistoricalEvent[];
  presence: AgentPresence[];
  messages: ChatMessage[];
}

interface RoomSummary {
  id: string;
  name: string;
}

interface Me {
  display_name?: string;
  username: string;
}

type Category = "task" | "handoff" | "other";

type TimelineEntry =
  | {
      id: string;
      kind: "card";
      timestamp: string;
      category: Category;
      label: string;
    }
  | { id: string; kind: "message"; timestamp: string; message: ChatMessage };

// Maps both the historical (TASK_CREATED) and live (TASK.CREATE) type
// formats to one label, so the timeline doesn't visually distinguish
// "happened before you connected" from "just happened" by format alone.
const TYPE_LABELS: Record<string, { category: Category; label: string }> = {
  TASK_CREATED: { category: "task", label: "Task created" },
  "TASK.CREATE": { category: "task", label: "Task created" },
  TASK_CLAIMED: { category: "task", label: "Task claimed" },
  "TASK.CLAIM": { category: "task", label: "Task claimed" },
  TASK_COMPLETED: { category: "task", label: "Task completed" },
  "TASK.COMPLETE": { category: "task", label: "Task completed" },
  HANDOFF_REQUESTED: { category: "handoff", label: "Handoff requested" },
  "HANDOFF.REQUEST": { category: "handoff", label: "Handoff requested" },
  HANDOFF_ACCEPTED: { category: "handoff", label: "Handoff accepted" },
  "HANDOFF.ACCEPT": { category: "handoff", label: "Handoff accepted" },
};

function classify(type: string): { category: Category; label: string } {
  return TYPE_LABELS[type] ?? { category: "other", label: type };
}

function fromHistorical(e: HistoricalEvent): TimelineEntry {
  const { category, label } = classify(e.type);
  return {
    id: `h-${e.id}`,
    kind: "card",
    category,
    label,
    timestamp: e.created_at,
  };
}

function fromEnvelope(e: Envelope): TimelineEntry {
  const { category, label } = classify(e.type);
  return { id: e.id, kind: "card", category, label, timestamp: e.timestamp };
}

function fromMessage(m: ChatMessage): TimelineEntry {
  return { id: m.id, kind: "message", timestamp: m.created_at, message: m };
}

const CATEGORY_STYLES: Record<Category, string> = {
  task: "border-[var(--room-task-blue)]/40 bg-[var(--room-task-blue)]/5",
  handoff:
    "border-[var(--room-handoff-purple)]/40 bg-[var(--room-handoff-purple)]/5",
  other: "border-[var(--login-border-strong)]",
};

// Failure messages have no dedicated wire field — ADR-004 frames a
// failed generation as a real agent message, not a separate concept
// (internal/message.Orchestrator.fail), so the one signal available is
// its own fixed prefix. Matched here, not re-invented: this is the
// exact string the backend writes.
const FAILURE_PREFIX = "I couldn't generate a reply —";
function isFailureMessage(content: string): boolean {
  return content.startsWith(FAILURE_PREFIX);
}

function snippet(content: string, max = 60): string {
  const oneLine = content.replace(/\s+/g, " ").trim();
  return oneLine.length > max ? oneLine.slice(0, max).trimEnd() + "…" : oneLine;
}

// dateKey/formatDateLabel back the timeline's date dividers — grouping
// by calendar day in the viewer's own local time, since that's what
// "Today" actually means to the person looking at it.
function dateKey(iso: string): string {
  const d = new Date(iso);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

function formatDateLabel(iso: string): string {
  const date = new Date(iso);
  const now = new Date();
  const startOfDay = (d: Date) =>
    new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const diffDays = Math.round(
    (startOfDay(now) - startOfDay(date)) / (24 * 60 * 60 * 1000),
  );
  if (diffDays === 0) return "Today";
  if (diffDays === 1) return "Yesterday";
  return date.toLocaleDateString(undefined, {
    month: "long",
    day: "numeric",
    year: date.getFullYear() !== now.getFullYear() ? "numeric" : undefined,
  });
}

// How close to the bottom (in pixels) still counts as "at the bottom" —
// generous enough that sub-pixel layout rounding never falsely shows the
// scroll-to-latest pill while the view is, for all practical purposes,
// already caught up.
const BOTTOM_THRESHOLD = 48;

// Per-character delay for the generated-title reveal — a simulated
// typing effect over the complete string the backend already returned
// in one piece (ADR-004's addendum: no real token streaming here, same
// as reply generation), not a live stream from the model.
const TITLE_REVEAL_MS = 28;

export default function RoomViewPage() {
  const params = useParams();
  const roomId = Array.isArray(params.id) ? params.id[0] : params.id;

  const [entries, setEntries] = useState<TimelineEntry[]>([]);
  const [messagesById, setMessagesById] = useState<Record<string, ChatMessage>>(
    {},
  );
  const [presence, setPresence] = useState<Record<string, string>>({});
  const [agentNames, setAgentNames] = useState<Record<string, string>>({});
  const [agentProviders, setAgentProviders] = useState<Record<string, string>>(
    {},
  );
  const [roomAgents, setRoomAgents] = useState<RoomAgent[]>([]);
  const [roomAgentSummaries, setRoomAgentSummaries] = useState<
    RoomAgentSummary[]
  >([]);
  const [roomName, setRoomName] = useState<string>("");
  const [me, setMe] = useState<Me | null>(null);
  const [connection, setConnection] = useState<
    "connecting" | "open" | "reconnecting"
  >("connecting");
  const [artifact, setArtifact] = useState<ArtifactContent | null>(null);
  const [sendError, setSendError] = useState<string | null>(null);
  const [showScrollToLatest, setShowScrollToLatest] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  const [decisions, setDecisions] = useState<Decision[]>([]);
  const [pinError, setPinError] = useState<string | null>(null);

  const timelineRef = useRef<HTMLDivElement>(null);
  // Tracked in a ref, not state: the entries-changed effect below reads
  // this synchronously to decide whether to auto-follow a new arrival,
  // and a ref avoids that effect needing to depend on (and re-run
  // debounced against) scroll-driven state changes.
  const atBottomRef = useRef(true);
  const titleRevealTimer = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    void apiFetch<Me>("/v1/users/me")
      .then(setMe)
      .catch(() => setMe(null));
  }, []);

  useEffect(() => {
    if (!roomId) return;
    // Same data the sidebar already reads correctly (GET /v1/rooms) —
    // there's no single-room GET endpoint, and this room view is the
    // one other place in the app that needs a room's own name, so it
    // reads the same list and finds itself in it rather than inventing
    // a second source of truth for "what is this room called."
    void apiFetch<RoomSummary[]>("/v1/rooms")
      .then((rooms) => {
        const match = rooms.find((r) => r.id === roomId);
        if (match) setRoomName(match.name);
      })
      .catch(() => {});
  }, [roomId]);

  // Full agent objects, not just {id, name}: the info panel wants
  // provider + capabilities too, and the cost pill needs provider to
  // price each agent's usage. One fetch, three consumers (this, the
  // composer's @-picker via roomAgents, the pill's per-message pricing
  // via agentProviders) rather than three separate calls. Extracted out
  // of its own effect so AddAgentMenu's onAdded can re-run this same
  // fetch after registering a new agent — the same reload-after-mutation
  // convention every other mutation on this page already follows
  // (Sidebar's loadRooms, this page's own loadDecisions below), rather
  // than hand-splicing the new agent into four separate state shapes.
  const loadRoomAgents = () => {
    if (!roomId) return;
    void apiFetch<
      { id: string; name: string; provider: string; capabilities: string[] }[]
    >(`/v1/rooms/${roomId}/agents`)
      .then((agents) => {
        setRoomAgents(
          agents.map((a) => ({ id: a.id, name: a.name, provider: a.provider })),
        );
        setRoomAgentSummaries(
          agents.map((a) => ({
            id: a.id,
            name: a.name,
            provider: a.provider,
            capabilities: a.capabilities,
          })),
        );
        setAgentNames((prev) => {
          const next = { ...prev };
          for (const a of agents) next[a.id] = a.name;
          return next;
        });
        setAgentProviders((prev) => {
          const next = { ...prev };
          for (const a of agents) next[a.id] = a.provider;
          return next;
        });
      })
      .catch(() => {
        setRoomAgents([]);
        setRoomAgentSummaries([]);
      });
  };

  useEffect(loadRoomAgents, [roomId]);

  const loadDecisions = () => {
    if (!roomId) return;
    void apiFetch<Decision[]>(`/v1/rooms/${roomId}/decisions`)
      .then(setDecisions)
      .catch(() => {});
  };

  useEffect(loadDecisions, [roomId]);

  useEffect(() => {
    if (!roomId) return;

    // Subscribing for updates from an external system (the SSE stream)
    // and calling setState from its own callbacks is exactly the effect
    // pattern react.dev recommends — unlike a plain fetch-on-mount, this
    // doesn't trip the set-state-in-effect rule since nothing is set
    // synchronously in the effect body itself.
    const source = new EventSource(apiUrl(`/v1/rooms/${roomId}/stream`), {
      withCredentials: true,
    });

    source.addEventListener("snapshot", (e) => {
      const data = JSON.parse((e as MessageEvent).data) as Snapshot;
      // Defensive, not just trusting the backend: a Go nil slice encodes
      // as JSON null, not [], so a brand-new room with nothing in it yet
      // is exactly the case this needs to survive.
      const cardEntries = (data.events ?? []).map(fromHistorical);
      const messageEntries = (data.messages ?? []).map(fromMessage);
      const merged = [...cardEntries, ...messageEntries].sort((a, b) =>
        a.timestamp < b.timestamp ? -1 : a.timestamp > b.timestamp ? 1 : 0,
      );
      setEntries(merged);

      const byId: Record<string, ChatMessage> = {};
      for (const m of data.messages ?? []) byId[m.id] = m;
      setMessagesById(byId);

      const initial: Record<string, string> = {};
      for (const p of data.presence ?? []) {
        initial[p.agent_id] = p.status;
      }
      setPresence(initial);
      setConnection("open");
    });

    source.addEventListener("event", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (msg.event) {
        setEntries((prev) => [...prev, fromEnvelope(msg.event!)]);
      }
    });

    source.addEventListener("presence", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (msg.presence) {
        const { agent_id, status } = msg.presence;
        setPresence((prev) => ({ ...prev, [agent_id]: status }));
      }
    });

    source.addEventListener("message", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (msg.message) {
        const m = msg.message;
        setEntries((prev) => [...prev, fromMessage(m)]);
        setMessagesById((prev) => ({ ...prev, [m.id]: m }));
      }
    });

    source.addEventListener("room", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (!msg.room) return;
      const finalName = msg.room.name;

      // A "room" event only ever arrives here as the auto-title job's
      // successful outcome — the race guard on the backend discards and
      // never publishes when a manual rename already won, so receiving
      // this at all means it's safe to reveal. The reveal is a simulated
      // per-character typing animation over the complete string already
      // returned, not a real stream (ADR-004's addendum).
      if (titleRevealTimer.current) clearInterval(titleRevealTimer.current);
      let i = 0;
      titleRevealTimer.current = setInterval(() => {
        i++;
        setRoomName(finalName.slice(0, i));
        if (i >= finalName.length && titleRevealTimer.current) {
          clearInterval(titleRevealTimer.current);
          titleRevealTimer.current = null;
        }
      }, TITLE_REVEAL_MS);

      // Sidebar has no SSE subscription of its own to catch this — it
      // isn't scoped to any one room — so it's told the same way it
      // already reloads after every other room mutation it performs
      // itself (pin/rename/delete): a plain DOM event it listens for.
      window.dispatchEvent(
        new CustomEvent("harmonia:room-updated", { detail: msg.room }),
      );
    });

    // EventSource retries on its own; a drop just means "not open right
    // now," not "give up" — reflected as "Reconnecting…" below, not an
    // error state.
    source.onerror = () => {
      setConnection((c) => (c === "connecting" ? c : "reconnecting"));
    };

    return () => {
      source.close();
      if (titleRevealTimer.current) clearInterval(titleRevealTimer.current);
    };
  }, [roomId]);

  useEffect(() => {
    const el = timelineRef.current;
    if (!el) return;
    // Only auto-follow new entries when the view was already at the
    // bottom — otherwise a message arriving while someone has scrolled
    // up to read history would yank them back down. The scroll-to-latest
    // pill (driven by the same atBottomRef, via handleScroll below)
    // covers that case instead: it's already showing since atBottomRef
    // is false, and clicking it does the (smooth) catch-up scroll.
    if (atBottomRef.current) {
      el.scrollTo({ top: el.scrollHeight });
    }
  }, [entries]);

  const handleScroll = () => {
    const el = timelineRef.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    const atBottom = distanceFromBottom < BOTTOM_THRESHOLD;
    atBottomRef.current = atBottom;
    setShowScrollToLatest(!atBottom);
  };

  const scrollToLatest = () => {
    const el = timelineRef.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    atBottomRef.current = true;
    setShowScrollToLatest(false);
  };

  const humanName = me?.display_name || me?.username || "You";

  const typingAgentIds = Object.entries(presence)
    .filter(([, status]) => status === "running")
    .map(([agentId]) => agentId);

  const handleSend = async (
    content: string,
    mentionedAgentId: string | null,
  ) => {
    if (!roomId) return;
    setSendError(null);
    try {
      await apiFetch(`/v1/rooms/${roomId}/messages`, {
        method: "POST",
        body: {
          content,
          ...(mentionedAgentId ? { mentioned_agent_id: mentionedAgentId } : {}),
        },
      });
      // No optimistic local insert: the POST's own publish arrives back
      // over the same SSE stream this page already renders from
      // (hub.Publish happens after commit, before the response even
      // returns) — adding it a second time here would risk a duplicate
      // render if the SSE event wins the race, which it normally will.
    } catch (err) {
      setSendError(
        err instanceof Error ? err.message : "Failed to send message.",
      );
    }
  };

  const handlePinDecision = async (messageId: string) => {
    if (!roomId) return;
    setPinError(null);
    try {
      const decision = await apiFetch<Decision>(
        `/v1/rooms/${roomId}/messages/${messageId}/decisions`,
        { method: "POST" },
      );
      // Direct response, not the SSE stream: pinning has no realtime
      // fan-out (see decision.Store's own doc comment — this is a
      // same-session action a human takes, not something another
      // viewer needs pushed live), so appending the actual response is
      // the source of truth here, not an optimistic guess.
      setDecisions((prev) =>
        prev.some((d) => d.id === decision.id) ? prev : [...prev, decision],
      );
    } catch (err) {
      setPinError(
        err instanceof Error ? err.message : "Failed to pin decision.",
      );
    }
  };

  // Objective: the room's first message, per the build brief's explicit
  // scope call — "no new capture needed," not a real captured field.
  const firstMessageEntry = entries.find((e) => e.kind === "message");
  const objective =
    firstMessageEntry?.kind === "message"
      ? firstMessageEntry.message.content
      : null;

  const pinnedMessageIds = new Set(decisions.map((d) => d.message_id));

  // Cost/token pill totals — accumulated client-side from messages
  // already in state (snapshot + live SSE), not a separate backend
  // aggregation endpoint: every ChatMessage already carries its own
  // real input_tokens/output_tokens (internal/message.Message), so
  // there's nothing a server-side sum would provide that summing what's
  // already loaded doesn't. See lib/tokenPricing.ts for why the dollar
  // figure is a rough, clearly-non-authoritative estimate.
  let totalInputTokens = 0;
  let totalOutputTokens = 0;
  let totalCostUSD = 0;
  for (const entry of entries) {
    if (entry.kind !== "message") continue;
    const m = entry.message;
    if (m.input_tokens == null || m.output_tokens == null) continue;
    totalInputTokens += m.input_tokens;
    totalOutputTokens += m.output_tokens;
    const providerName = m.agent_id ? agentProviders[m.agent_id] : undefined;
    if (providerName) {
      totalCostUSD += estimateCostUSD(
        providerName,
        m.input_tokens,
        m.output_tokens,
      );
    }
  }
  const hasUsageData = totalInputTokens > 0 || totalOutputTokens > 0;

  const rendered: ReactNode[] = [];
  let lastDateKey: string | null = null;
  entries.forEach((entry, i) => {
    const key = dateKey(entry.timestamp);
    if (key !== lastDateKey) {
      lastDateKey = key;
      rendered.push(
        <div
          key={`divider-${key}`}
          className="flex items-center gap-3 text-[12px] text-[var(--login-text-muted)] before:h-px before:flex-1 before:bg-[var(--login-border)] after:h-px after:flex-1 after:bg-[var(--login-border)]"
        >
          <span className="font-[family-name:var(--login-font-mono)]">
            {formatDateLabel(entry.timestamp)}
          </span>
        </div>,
      );
    }

    if (entry.kind === "card") {
      rendered.push(
        <div
          key={entry.id}
          className={`ml-[42px] rounded-[10px] border p-3.5 text-sm ${CATEGORY_STYLES[entry.category]}`}
        >
          <div className="font-medium text-[var(--login-text)]">
            {entry.label}
          </div>
          <div className="text-xs text-[var(--login-text-muted)]">
            {new Date(entry.timestamp).toLocaleTimeString()}
          </div>
        </div>,
      );
      return;
    }

    const m = entry.message;
    const senderName = displaySenderName(m, agentNames, humanName);

    // "Replying to X" earns its place specifically when the
    // conversation moved on before the reply arrived — i.e. it's not
    // simply answering whatever came right before it in the timeline
    // (of any kind, not just another message).
    const prev = entries[i - 1];
    const immediatelyAfterItsOwnMention =
      prev?.kind === "message" && prev.message.id === m.reply_to_message_id;
    const referenced = m.reply_to_message_id
      ? messagesById[m.reply_to_message_id]
      : undefined;
    const replyPreview =
      referenced && !immediatelyAfterItsOwnMention
        ? {
            senderName: displaySenderName(referenced, agentNames, humanName),
            snippet: snippet(referenced.content),
          }
        : undefined;

    rendered.push(
      <MessageRow
        key={entry.id}
        message={{ ...m, failed: isFailureMessage(m.content) }}
        senderName={senderName}
        senderProvider={m.agent_id ? agentProviders[m.agent_id] : undefined}
        replyPreview={replyPreview}
        onOpenArtifact={setArtifact}
        pinned={pinnedMessageIds.has(m.id)}
        onPinDecision={() => void handlePinDecision(m.id)}
      />,
    );
  });

  return (
    <div className="flex h-full min-w-0 flex-1">
      <main className="relative flex min-w-0 flex-1 flex-col">
        <div className="flex shrink-0 items-center justify-between border-b border-[var(--login-border)] px-6 py-3.5">
          <h1 className="truncate text-[16px] font-semibold text-[var(--login-text)]">
            {roomName || "…"}
          </h1>
          <div className="flex shrink-0 items-center gap-3.5">
            {hasUsageData && (
              <span
                title="Estimated cost — not authoritative, see this room's actual token usage on your provider's own dashboard for a real figure"
                className="flex items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-2.5 py-1 font-[family-name:var(--login-font-mono)] text-[12px] text-[var(--login-text-muted)]"
              >
                <CoinIcon />
                {formatCostUSD(totalCostUSD)} ·{" "}
                {formatTokenCount(totalInputTokens + totalOutputTokens)} tokens
              </span>
            )}
            <button
              type="button"
              title="Room info"
              onClick={() => setInfoOpen((o) => !o)}
              className={`flex rounded-md p-1.5 ${
                infoOpen
                  ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
                  : "text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
              }`}
            >
              <InfoIcon />
            </button>
            <span className="text-[13px] text-[var(--login-text-muted)]">
              {connection === "open"
                ? "● Live"
                : connection === "reconnecting"
                  ? "○ Reconnecting…"
                  : "○ Connecting…"}
            </span>
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-[var(--login-border)] px-6 py-2">
          <span className="font-[family-name:var(--login-font-mono)] text-[11px] text-[var(--login-text-muted)]">
            Agents:
          </span>
          {roomAgentSummaries.map((a) => (
            <span
              key={a.id}
              className="flex items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] py-0.5 pl-1 pr-2.5 text-[12px] text-[var(--login-text-secondary)]"
            >
              <span className="flex h-4 w-4 items-center justify-center rounded-full bg-[var(--login-border-strong)] text-[8px] font-semibold text-[var(--login-accent)]">
                <AgentAvatarGlyph provider={a.provider} name={a.name} size={9} />
              </span>
              {a.name}
            </span>
          ))}
          {roomId && (
            <AddAgentMenu
              roomId={roomId}
              onAdded={loadRoomAgents}
              label={roomAgentSummaries.length === 0 ? "Add an agent to get started" : "Add agent"}
            />
          )}
        </div>

        <div
          ref={timelineRef}
          onScroll={handleScroll}
          className="no-scrollbar flex-1 overflow-y-auto"
        >
          <div className="mx-auto flex w-full max-w-[720px] flex-col gap-5 px-6 py-6">
            {entries.length === 0 && typingAgentIds.length === 0 && (
              <p className="text-sm text-[var(--login-text-muted)]">
                No activity yet — @mention an agent below to get started.
              </p>
            )}

            {rendered}

            {typingAgentIds.map((agentId) => (
              <TypingIndicator
                key={agentId}
                agentName={agentNames[agentId] || "Agent"}
              />
            ))}
          </div>
        </div>

        {showScrollToLatest && (
          <button
            type="button"
            onClick={scrollToLatest}
            className="absolute bottom-[104px] left-1/2 flex -translate-x-1/2 items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] py-1.5 pl-3 pr-2.5 text-[12.5px] text-[var(--login-text-secondary)] shadow-[0_4px_16px_rgba(0,0,0,0.3)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
          >
            New messages
            <ChevronDownIcon />
          </button>
        )}

        {(sendError || pinError) && (
          <p className="mx-auto w-full max-w-[720px] px-6 text-[13px] text-[var(--room-warn)]">
            {sendError || pinError}
          </p>
        )}

        <Composer
          agents={roomAgents}
          onSend={(c, a) => void handleSend(c, a)}
        />
      </main>

      <RoomInfoPanel
        open={infoOpen}
        onClose={() => setInfoOpen(false)}
        objective={objective}
        agents={roomAgentSummaries}
        decisions={decisions}
        roomId={roomId ?? ""}
        onAgentAdded={loadRoomAgents}
      />
      <ArtifactPanel artifact={artifact} onClose={() => setArtifact(null)} />
    </div>
  );
}
