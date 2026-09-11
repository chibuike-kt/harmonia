"use client";

import type { ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import { useParams } from "next/navigation";
import { apiFetch, apiUrl } from "@/lib/api";
import {
  ArtifactPanel,
  type ArtifactContent,
} from "@/components/ArtifactPanel";
import {
  ArtifactsMenu,
  type ArtifactListItem,
} from "@/components/ArtifactsMenu";
import { AddAgentMenu } from "@/components/AddAgentMenu";
import { ApprovalCard } from "@/components/ApprovalCard";
import { HandoffCard } from "@/components/HandoffCard";
import { TaskCard } from "@/components/TaskCard";
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
import { collectRoomArtifacts } from "@/lib/messageContent";
import { AgentAvatarGlyph } from "@/components/providerLogos";
import { Tooltip } from "@/components/Tooltip";
import {
  estimateCostUSD,
  formatCostUSD,
  formatTokenCount,
} from "@/lib/tokenPricing";

// internal/message.go's own recencyLimit — ListByRoom (the snapshot's
// message source) never returns more than this many of a room's most
// recent messages. Duplicated here, not imported (no shared-constant
// mechanism across the Go/TS boundary in this codebase), purely so the
// artifacts menu can tell whether the initial snapshot might have cut
// off older messages — if it changes on the backend, update this too.
const RECENCY_LIMIT_HINT = 50;

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

interface RoomObjectiveUpdate {
  room_id: string;
  objective: string;
}

interface PickupUsage {
  room_id: string;
  agent_id: string;
  input_tokens: number;
  output_tokens: number;
}

interface RealtimeMessage {
  kind: "event" | "presence" | "message" | "room" | "pickup_usage" | "objective";
  event?: Envelope;
  presence?: AgentPresence;
  message?: ChatMessage;
  room?: RoomUpdate;
  pickup_usage?: PickupUsage;
  objective?: RoomObjectiveUpdate;
}

interface Snapshot {
  events: HistoricalEvent[];
  presence: AgentPresence[];
  messages: ChatMessage[];
  // ADR-007 batch B: this room's running total phase-1 classification
  // spend as of connect time — the cost pill's seed value. Counted
  // toward the pill's token total, but not its $ estimate: a snapshot
  // aggregate has no per-agent/provider breakdown to price accurately
  // (unlike a live pickup_usage event, which does), so a reload-time
  // total stays honestly token-only rather than guessing a blended
  // rate. See the cost pill's own comment where this is summed.
  pickup_eval_input_tokens: number;
  pickup_eval_output_tokens: number;
}

interface RoomSummary {
  id: string;
  name: string;
  agent_cascading_enabled: boolean;
  autonomous_pickup_enabled: boolean;
  // omitempty on the Go side — absent, not null, when the room has no
  // objective yet.
  objective?: string;
}

interface Me {
  display_name?: string;
  username: string;
}

type Category = "task" | "handoff" | "other";

// HandoffInfo is only ever set on a "card" entry whose type is
// HANDOFF_REQUESTED/HANDOFF.REQUEST — the one handoff transition with a
// real payload to render from (see internal/handoff/http.go's own
// AcceptHandler, which publishes an empty payload — nothing to enrich an
// "accepted" card with today, so that one stays the plain label
// fallback). fromAgentId/toAgentId/summary/risks are exactly what
// internal/handoff.RequestHandler's own envelope carries, and what
// internal/actionproposal's executeApprovedHandoff publishes on
// approval too — one payload shape, one place that knows how to render it.
interface HandoffInfo {
  fromAgentId: string;
  toAgentId?: string;
  summary?: string;
  risks?: string[];
}

type TimelineEntry =
  | {
      id: string;
      kind: "card";
      timestamp: string;
      category: Category;
      label: string;
      handoff?: HandoffInfo;
    }
  | { id: string; kind: "message"; timestamp: string; message: ChatMessage }
  | {
      id: string;
      kind: "approval";
      timestamp: string;
      proposalId: string;
      proposingAgentId: string;
      actionType: string;
      payload: Record<string, unknown>;
      // pending until an ACTION_RESOLVED/ACTION.RESOLVE for this same
      // proposal_id updates this entry in place — never a second,
      // separate entry (see the snapshot handler and the "event"
      // listener below, both of which enforce that by construction).
      status: "pending" | "approved" | "rejected";
    }
  | {
      id: string;
      kind: "task";
      timestamp: string;
      taskId: string;
      objective: string;
      // Only the three transitions this codebase's own task.Store
      // actually performs (Create inserts QUEUED directly, Claim moves
      // QUEUED -> CLAIMED, Complete moves CLAIMED -> COMPLETED) — never
      // a second entry for the same taskId once TASK_CLAIMED/
      // TASK_COMPLETED update this one in place, same as "approval".
      status: "QUEUED" | "CLAIMED" | "COMPLETED";
      ownerAgentId?: string;
    };

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

function buildHandoffInfo(
  type: string,
  fromAgentId: string | undefined,
  payload: Record<string, unknown>,
): HandoffInfo | undefined {
  if (
    (type !== "HANDOFF_REQUESTED" && type !== "HANDOFF.REQUEST") ||
    !fromAgentId
  ) {
    return undefined;
  }
  return {
    fromAgentId,
    toAgentId: payload.to_agent_id as string | undefined,
    summary: payload.summary as string | undefined,
    risks: payload.risks as string[] | undefined,
  };
}

function fromHistorical(e: HistoricalEvent): TimelineEntry {
  const { category, label } = classify(e.type);
  return {
    id: `h-${e.id}`,
    kind: "card",
    category,
    label,
    timestamp: e.created_at,
    handoff: buildHandoffInfo(e.type, e.agent_id, e.payload),
  };
}

// ACTION_PROPOSED/ACTION.PROPOSE don't route through classify()'s
// generic card treatment the way every other event type does — a
// proposal needs Approve/Reject affordances while pending, and a
// resolved-state badge once it isn't, not a plain historical label —
// so it becomes its own "approval" timeline entry instead.
// ACTION_RESOLVED/ACTION.RESOLVE never produce their own entry at all:
// both the snapshot handler and the live "event" listener update the
// matching "approval" entry (by proposal_id) in place instead — the
// same card throughout its life, never replaced or removed.
function fromProposedEvent(
  id: string,
  timestamp: string,
  proposingAgentId: string,
  payload: Record<string, unknown>,
): TimelineEntry {
  return {
    id: `proposal-${payload.proposal_id}`,
    kind: "approval",
    timestamp,
    proposalId: payload.proposal_id as string,
    proposingAgentId,
    actionType: payload.action_type as string,
    payload,
    status: "pending",
  };
}

// TASK_CREATED/TASK.CREATE become their own "task" timeline entry for
// the same reason ACTION_PROPOSED does — a real title/status card, not
// a plain historical label. TASK_CLAIMED/TASK_COMPLETED (and their live
// TASK.CLAIM/TASK.COMPLETE forms) never produce their own entry: both
// the snapshot handler and the live "event" listener update the
// matching "task" entry (by task_id — a real column on every task
// event, not something parsed out of payload) in place instead.
function fromCreatedTaskEvent(
  id: string,
  timestamp: string,
  taskId: string,
  payload: Record<string, unknown>,
): TimelineEntry {
  return {
    id: `task-${taskId}`,
    kind: "task",
    timestamp,
    taskId,
    objective: (payload.objective as string) || "",
    status: "QUEUED",
  };
}

function fromEnvelope(e: Envelope): TimelineEntry {
  if (e.type === "ACTION.PROPOSE") {
    return fromProposedEvent(e.id, e.timestamp, e.sender.agent_id, e.payload);
  }
  if (e.type === "TASK.CREATE" && e.task_id) {
    return fromCreatedTaskEvent(e.id, e.timestamp, e.task_id, e.payload);
  }
  const { category, label } = classify(e.type);
  return {
    id: e.id,
    kind: "card",
    category,
    label,
    timestamp: e.timestamp,
    handoff: buildHandoffInfo(e.type, e.sender.agent_id, e.payload),
  };
}

function fromMessage(m: ChatMessage): TimelineEntry {
  return { id: m.id, kind: "message", timestamp: m.created_at, message: m };
}

// buildEventEntries turns the snapshot's full historical event list (no
// recency cap, unlike messages — see event.Store.ListByRoom) into
// timeline entries, correlating ACTION_PROPOSED with its later
// ACTION_RESOLVED (if any) into the one "approval" entry at its
// original position rather than two separate entries: events are
// already chronological (created_at ASC), so a proposal's own
// ACTION_PROPOSED is guaranteed to appear before any ACTION_RESOLVED
// for it, making a single left-to-right pass sufficient.
function buildEventEntries(events: HistoricalEvent[]): TimelineEntry[] {
  const entries: TimelineEntry[] = [];
  const approvalsByProposalId = new Map<
    string,
    Extract<TimelineEntry, { kind: "approval" }>
  >();
  const tasksByTaskId = new Map<
    string,
    Extract<TimelineEntry, { kind: "task" }>
  >();
  for (const e of events) {
    if (e.type === "ACTION_PROPOSED") {
      const entry = fromProposedEvent(
        `h-${e.id}`,
        e.created_at,
        e.agent_id ?? "",
        e.payload,
      ) as Extract<TimelineEntry, { kind: "approval" }>;
      approvalsByProposalId.set(entry.proposalId, entry);
      entries.push(entry);
      continue;
    }
    if (e.type === "ACTION_RESOLVED") {
      const proposalId = e.payload.proposal_id as string;
      const existing = approvalsByProposalId.get(proposalId);
      if (existing) {
        existing.status = e.payload.status as "approved" | "rejected";
      }
      continue;
    }
    if (e.type === "TASK_CREATED" && e.task_id) {
      const entry = fromCreatedTaskEvent(
        `h-${e.id}`,
        e.created_at,
        e.task_id,
        e.payload,
      ) as Extract<TimelineEntry, { kind: "task" }>;
      tasksByTaskId.set(entry.taskId, entry);
      entries.push(entry);
      continue;
    }
    if (e.type === "TASK_CLAIMED" && e.task_id) {
      const existing = tasksByTaskId.get(e.task_id);
      if (existing) {
        existing.status = "CLAIMED";
        existing.ownerAgentId = e.agent_id;
      }
      continue;
    }
    if (e.type === "TASK_COMPLETED" && e.task_id) {
      const existing = tasksByTaskId.get(e.task_id);
      if (existing) {
        existing.status = "COMPLETED";
      }
      continue;
    }
    entries.push(fromHistorical(e));
  }
  return entries;
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
  // Mirrored from agentProviders below — the SSE effect mounts once per
  // roomId (see its own dependency array) and its pickup_usage listener
  // needs whatever agentProviders is at the moment an event actually
  // arrives, not whatever it was when that effect last ran; a plain
  // closure over the state variable would go stale the instant agents
  // load in afterward.
  const agentProvidersRef = useRef<Record<string, string>>({});
  useEffect(() => {
    agentProvidersRef.current = agentProviders;
  }, [agentProviders]);
  const [roomAgents, setRoomAgents] = useState<RoomAgent[]>([]);
  const [roomAgentSummaries, setRoomAgentSummaries] = useState<
    RoomAgentSummary[]
  >([]);
  const [roomName, setRoomName] = useState<string>("");
  const [roomObjective, setRoomObjective] = useState<string | null>(null);
  const [agentCascadingEnabled, setAgentCascadingEnabled] = useState(false);
  const [autonomousPickupEnabled, setAutonomousPickupEnabled] = useState(false);
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
  const [retryError, setRetryError] = useState<string | null>(null);
  const [resolvingProposalId, setResolvingProposalId] = useState<string | null>(
    null,
  );
  const [initialMessageCount, setInitialMessageCount] = useState<number | null>(
    null,
  );
  // ADR-007 batch B's own running total, separate from the per-message
  // sum below: a phase-1 classification is real spend but never a
  // ChatMessage (see internal/message.Store.RecordPickupEvaluationUsage's
  // own doc comment for why). costUSD only ever grows from a live
  // pickup_usage event, which carries a real agent_id to price against —
  // the snapshot's own seed has no such per-agent breakdown to price
  // accurately, so it seeds tokens only, not cost. See the cost pill's
  // own comment below for how both are combined.
  const [pickupUsage, setPickupUsage] = useState({
    inputTokens: 0,
    outputTokens: 0,
    costUSD: 0,
  });

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
        if (match) {
          setRoomName(match.name);
          setRoomObjective(match.objective ?? null);
          setAgentCascadingEnabled(match.agent_cascading_enabled);
          setAutonomousPickupEnabled(match.autonomous_pickup_enabled);
        }
      })
      .catch(() => {});
  }, [roomId]);

  const handleToggleCascading = async (enabled: boolean) => {
    if (!roomId) return;
    setAgentCascadingEnabled(enabled);
    try {
      await apiFetch(`/v1/rooms/${roomId}`, {
        method: "PATCH",
        body: { agent_cascading_enabled: enabled },
      });
    } catch {
      // Revert on failure — an optimistic toggle that silently didn't
      // take would leave the panel showing a setting the room doesn't
      // actually have.
      setAgentCascadingEnabled(!enabled);
    }
  };

  const handleTogglePickup = async (enabled: boolean) => {
    if (!roomId) return;
    setAutonomousPickupEnabled(enabled);
    try {
      await apiFetch(`/v1/rooms/${roomId}`, {
        method: "PATCH",
        body: { autonomous_pickup_enabled: enabled },
      });
    } catch {
      setAutonomousPickupEnabled(!enabled);
    }
  };

  // A manual edit here permanently stops the auto-generation job
  // (internal/message.ObjectiveGenerator) from ever overwriting it again
  // — same PATCH-and-protect pattern as a manual rename protecting Name.
  const handleEditObjective = async (objective: string) => {
    if (!roomId) return;
    const previous = roomObjective;
    setRoomObjective(objective);
    try {
      await apiFetch(`/v1/rooms/${roomId}`, {
        method: "PATCH",
        body: { objective },
      });
    } catch {
      setRoomObjective(previous);
    }
  };

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
      const cardEntries = buildEventEntries(data.events ?? []);
      const messageEntries = (data.messages ?? []).map(fromMessage);
      const merged = [...cardEntries, ...messageEntries].sort((a, b) =>
        a.timestamp < b.timestamp ? -1 : a.timestamp > b.timestamp ? 1 : 0,
      );
      setEntries(merged);
      // Captured once, from the snapshot alone (never touched again as
      // live messages append to entries below) — this is what tells the
      // artifacts menu whether the snapshot itself might have already
      // cut off older history, which a later, larger entries.length
      // couldn't answer on its own.
      setInitialMessageCount((data.messages ?? []).length);
      setPickupUsage({
        inputTokens: data.pickup_eval_input_tokens ?? 0,
        outputTokens: data.pickup_eval_output_tokens ?? 0,
        costUSD: 0,
      });

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
      if (!msg.event) return;
      // ACTION.RESOLVE (ADR-006 batch C) never becomes its own timeline
      // entry — it updates the "approval" entry ACTION.PROPOSE already
      // created, in place: same card, same position, its badge and
      // buttons swapping for a resolved-state badge (see ApprovalCard's
      // own resolution prop). It never disappears and never gets
      // replaced by a second entry — this is the one thing a page reload
      // (rebuilding the same state from buildEventEntries) must also
      // reproduce exactly, not just the live path.
      if (msg.event.type === "ACTION.RESOLVE") {
        const proposalId = msg.event.payload.proposal_id as string;
        const status = msg.event.payload.status as "approved" | "rejected";
        setEntries((prev) =>
          prev.map((entry) =>
            entry.kind === "approval" && entry.proposalId === proposalId
              ? { ...entry, status }
              : entry,
          ),
        );
        return;
      }
      // TASK.CLAIM/TASK.COMPLETE update the "task" entry TASK.CREATE
      // already produced, in place — same card, same position, its
      // status badge changing. Same reasoning as ACTION.RESOLVE above:
      // this is exactly what buildEventEntries must also reproduce from
      // history on reload, not just here on the live path.
      if (msg.event.type === "TASK.CLAIM" && msg.event.task_id) {
        const taskId = msg.event.task_id;
        const ownerAgentId = msg.event.sender.agent_id;
        setEntries((prev) =>
          prev.map((entry) =>
            entry.kind === "task" && entry.taskId === taskId
              ? { ...entry, status: "CLAIMED", ownerAgentId }
              : entry,
          ),
        );
        return;
      }
      if (msg.event.type === "TASK.COMPLETE" && msg.event.task_id) {
        const taskId = msg.event.task_id;
        setEntries((prev) =>
          prev.map((entry) =>
            entry.kind === "task" && entry.taskId === taskId
              ? { ...entry, status: "COMPLETED" }
              : entry,
          ),
        );
        return;
      }
      setEntries((prev) => [...prev, fromEnvelope(msg.event!)]);
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

    // An "objective" event only ever arrives here as
    // ObjectiveGenerator's own successful outcome — the race guard on
    // the backend discards and never publishes when a manual edit
    // already won, so receiving this at all means it's safe to apply.
    // Unlike the name's own typing-reveal animation, this just appears —
    // it's read-only prose in a side panel, not the room's own headline.
    source.addEventListener("objective", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (!msg.objective) return;
      setRoomObjective(msg.objective.objective);
    });

    // ADR-007 batch B: one phase-1 classification call's real token
    // usage, live — priced immediately since this event carries a real
    // agent_id to look up a provider for, unlike the snapshot's own
    // blended aggregate (see the Snapshot type's own comment).
    source.addEventListener("pickup_usage", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (!msg.pickup_usage) return;
      const { agent_id, input_tokens, output_tokens } = msg.pickup_usage;
      const providerName = agentProvidersRef.current[agent_id];
      const deltaCostUSD = providerName
        ? estimateCostUSD(providerName, input_tokens, output_tokens)
        : 0;
      setPickupUsage((prev) => ({
        inputTokens: prev.inputTokens + input_tokens,
        outputTokens: prev.outputTokens + output_tokens,
        costUSD: prev.costUSD + deltaCostUSD,
      }));
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

  const handleSend = async (content: string, mentionedAgentIds: string[]) => {
    if (!roomId) return;
    setSendError(null);
    try {
      await apiFetch(`/v1/rooms/${roomId}/messages`, {
        method: "POST",
        body: {
          content,
          ...(mentionedAgentIds.length > 0
            ? { mentioned_agent_ids: mentionedAgentIds }
            : {}),
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

  const handleRetryMessage = async (messageId: string) => {
    if (!roomId) return;
    setRetryError(null);
    try {
      await apiFetch(`/v1/rooms/${roomId}/messages/${messageId}/retry`, {
        method: "POST",
      });
      // No local state update here: the retried reply arrives as an
      // ordinary agent message over the SSE stream, the same as any
      // other reply — this call only has to kick the invocation off.
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : "Failed to retry.");
    }
  };

  const handleResolveProposal = async (
    proposalId: string,
    action: "approve" | "reject",
  ) => {
    setResolvingProposalId(proposalId);
    try {
      await apiFetch(`/v1/action_proposals/${proposalId}/${action}`, {
        method: "POST",
      });
      // Updated in place here directly rather than waiting on the
      // ACTION.RESOLVE event this same call also triggers over the
      // stream — the card's buttons shouldn't stay live for however long
      // that round-trip takes. The stream event still arrives and
      // applies the same update again (a no-op once status already
      // matches), which is what keeps a second browser tab on the same
      // room in sync too.
      const status = action === "approve" ? "approved" : "rejected";
      setEntries((prev) =>
        prev.map((entry) =>
          entry.kind === "approval" && entry.proposalId === proposalId
            ? { ...entry, status }
            : entry,
        ),
      );
    } catch {
      // Left in place on failure — the human can just try again, same as
      // any other action button in this app that doesn't have a
      // dedicated error surface of its own.
    } finally {
      setResolvingProposalId(null);
    }
  };

  const pinnedMessageIds = new Set(decisions.map((d) => d.message_id));

  // Artifacts menu: every file in the room — every fenced code block,
  // plus every message that's essentially one large pasted block — across
  // every message currently loaded, same detection MessageRow's own
  // inline chips use, just run across all of them instead of one. This
  // is a real, not hypothetical, gap: the SSE snapshot's message history
  // is capped (internal/message.go's recencyLimit), so a room with more
  // history than that cap will have older artifacts this list can't see
  // — mayBeIncomplete below reflects exactly that, surfaced as a plain
  // caption in the menu rather than silently presented as complete.
  const loadedMessages = entries
    .filter(
      (e): e is Extract<TimelineEntry, { kind: "message" }> =>
        e.kind === "message",
    )
    .map((e) => e.message);
  const artifactListItems: ArtifactListItem[] = collectRoomArtifacts(
    loadedMessages,
  ).map((a) => {
    const source = loadedMessages.find((m) => m.id === a.messageId)!;
    return {
      id: a.id,
      senderName: displaySenderName(source, agentNames, humanName),
      createdAt: source.created_at,
      kind: a.kind,
      language: a.language,
      lines: a.lines,
      code: a.code,
      suggestedName: a.suggestedName,
    };
  });
  const artifactsMayBeIncomplete =
    initialMessageCount !== null && initialMessageCount >= RECENCY_LIMIT_HINT;

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
  // ADR-007 batch B: phase-1 classification calls are real spend too
  // (build brief item 7) — folded into the same pill total, not a
  // separate figure, even though they're never ChatMessage rows (see
  // the pickupUsage state's own comment on why its cost component can
  // lag its token component slightly for pre-existing, reload-time
  // usage specifically).
  totalInputTokens += pickupUsage.inputTokens;
  totalOutputTokens += pickupUsage.outputTokens;
  totalCostUSD += pickupUsage.costUSD;
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
      if (entry.category === "handoff" && entry.handoff) {
        const fromName = agentNames[entry.handoff.fromAgentId] || "An agent";
        const toName = entry.handoff.toAgentId
          ? agentNames[entry.handoff.toAgentId] || "another agent"
          : "another agent";
        rendered.push(
          <HandoffCard
            key={entry.id}
            status="REQUESTED"
            fromName={fromName}
            toName={toName}
            title={entry.handoff.summary || entry.label}
            meta={
              entry.handoff.risks?.[0]
                ? `Risk noted: ${entry.handoff.risks[0]}`
                : undefined
            }
          />,
        );
        return;
      }
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

    if (entry.kind === "task") {
      const ownerName = entry.ownerAgentId
        ? agentNames[entry.ownerAgentId]
        : undefined;
      rendered.push(
        <TaskCard
          key={entry.id}
          status={entry.status}
          title={entry.objective}
          meta={
            ownerName
              ? `${ownerName} · ${entry.status === "COMPLETED" ? "completed" : "claimed"}`
              : undefined
          }
        />,
      );
      return;
    }

    if (entry.kind === "approval") {
      // Only request_handoff exists today (ADR-006 batch C) — objective/
      // to_agent_id/summary are exactly what executeRequestHandoff's own
      // ACTION.PROPOSE envelope carries.
      const fromName = agentNames[entry.proposingAgentId] || "An agent";
      const toAgentId = entry.payload.to_agent_id as string | undefined;
      const toName = toAgentId
        ? agentNames[toAgentId] || "another agent"
        : "another agent";
      const objective = (entry.payload.objective as string) || "a task";
      const summary = (entry.payload.summary as string) || "";
      rendered.push(
        <ApprovalCard
          key={entry.id}
          title={`Hand off "${objective}" to ${toName}`}
          meta={`${fromName}${summary ? `: ${summary}` : ""}`}
          pending={resolvingProposalId === entry.proposalId}
          resolution={entry.status === "pending" ? undefined : entry.status}
          onApprove={() =>
            void handleResolveProposal(entry.proposalId, "approve")
          }
          onReject={() =>
            void handleResolveProposal(entry.proposalId, "reject")
          }
        />,
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
        mentionedAgentNames={m.mentioned_agent_ids?.map(
          (id) => agentNames[id] || "Agent",
        )}
        replyPreview={replyPreview}
        onOpenArtifact={setArtifact}
        pinned={pinnedMessageIds.has(m.id)}
        onPinDecision={() => void handlePinDecision(m.id)}
        onRetry={
          m.sender_kind === "agent"
            ? () => void handleRetryMessage(m.id)
            : undefined
        }
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
              <Tooltip
                label="Estimated cost — not authoritative, see this room's actual token usage on your provider's own dashboard for a real figure"
                wrap
              >
                <span className="flex items-center gap-1.5 rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-2.5 py-1 font-[family-name:var(--login-font-mono)] text-[12px] text-[var(--login-text-muted)]">
                  <CoinIcon />
                  {formatCostUSD(totalCostUSD)} ·{" "}
                  {formatTokenCount(totalInputTokens + totalOutputTokens)}{" "}
                  tokens
                </span>
              </Tooltip>
            )}
            <ArtifactsMenu
              artifacts={artifactListItems}
              mayBeIncomplete={artifactsMayBeIncomplete}
              onOpenArtifact={setArtifact}
            />
            <Tooltip label={infoOpen ? "Hide room info" : "Room info"}>
              <button
                type="button"
                onClick={() => setInfoOpen((o) => !o)}
                className={`flex rounded-md p-1.5 ${
                  infoOpen
                    ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
                    : "text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
                }`}
              >
                <InfoIcon />
              </button>
            </Tooltip>
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
                <AgentAvatarGlyph
                  provider={a.provider}
                  name={a.name}
                  size={9}
                />
              </span>
              {a.name}
            </span>
          ))}
          {roomId && (
            <AddAgentMenu
              roomId={roomId}
              onAdded={loadRoomAgents}
              label={
                roomAgentSummaries.length === 0
                  ? "Add an agent to get started"
                  : "Add agent"
              }
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

        {(sendError || pinError || retryError) && (
          <p className="mx-auto w-full max-w-[720px] px-6 text-[13px] text-[var(--room-warn)]">
            {sendError || pinError || retryError}
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
        objective={roomObjective}
        onEditObjective={(o) => void handleEditObjective(o)}
        agents={roomAgentSummaries}
        decisions={decisions}
        roomId={roomId ?? ""}
        onAgentAdded={loadRoomAgents}
        agentCascadingEnabled={agentCascadingEnabled}
        onToggleCascading={handleToggleCascading}
        autonomousPickupEnabled={autonomousPickupEnabled}
        onTogglePickup={handleTogglePickup}
      />
      <ArtifactPanel artifact={artifact} onClose={() => setArtifact(null)} />
    </div>
  );
}
