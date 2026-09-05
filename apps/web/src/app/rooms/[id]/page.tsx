"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { apiUrl } from "@/lib/api";

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

interface RealtimeMessage {
  kind: "event" | "presence";
  event?: Envelope;
  presence?: AgentPresence;
}

interface Snapshot {
  events: HistoricalEvent[];
  presence: AgentPresence[];
}

type Category = "task" | "handoff" | "other";

interface TimelineItem {
  id: string;
  category: Category;
  label: string;
  timestamp: string;
}

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

function fromHistorical(e: HistoricalEvent): TimelineItem {
  const { category, label } = classify(e.type);
  return { id: `h-${e.id}`, category, label, timestamp: e.created_at };
}

function fromEnvelope(e: Envelope): TimelineItem {
  const { category, label } = classify(e.type);
  return { id: e.id, category, label, timestamp: e.timestamp };
}

const CATEGORY_STYLES: Record<Category, string> = {
  task: "border-blue-500/40 bg-blue-500/5",
  handoff: "border-purple-500/40 bg-purple-500/5",
  other: "border-foreground/20",
};

export default function RoomViewPage() {
  const params = useParams();
  const roomId = Array.isArray(params.id) ? params.id[0] : params.id;

  const [items, setItems] = useState<TimelineItem[]>([]);
  const [presence, setPresence] = useState<Record<string, string>>({});
  const [connection, setConnection] = useState<
    "connecting" | "open" | "reconnecting"
  >("connecting");

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
      setItems([...(data.events ?? [])].reverse().map(fromHistorical));
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
        const item = fromEnvelope(msg.event);
        setItems((prev) => [item, ...prev]);
      }
    });

    source.addEventListener("presence", (e) => {
      const msg = JSON.parse((e as MessageEvent).data) as RealtimeMessage;
      if (msg.presence) {
        const { agent_id, status } = msg.presence;
        setPresence((prev) => ({ ...prev, [agent_id]: status }));
      }
    });

    // EventSource retries on its own; a drop just means "not open right
    // now," not "give up" — reflected as "Reconnecting…" below, not an
    // error state.
    source.onerror = () => {
      setConnection((c) => (c === "connecting" ? c : "reconnecting"));
    };

    return () => source.close();
  }, [roomId]);

  return (
    <main className="mx-auto flex w-full max-w-2xl flex-1 flex-col gap-6 p-8">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Room</h1>
        <span className="text-foreground/50 text-sm">
          {connection === "open"
            ? "● Live"
            : connection === "reconnecting"
              ? "○ Reconnecting…"
              : "○ Connecting…"}
        </span>
      </div>

      {Object.keys(presence).length > 0 && (
        <div className="flex flex-wrap gap-2">
          {Object.entries(presence).map(([agentId, status]) => (
            <span
              key={agentId}
              className="rounded-full border border-foreground/20 px-3 py-1 text-xs"
            >
              agent {agentId.slice(0, 8)} · {status}
            </span>
          ))}
        </div>
      )}

      <div className="flex flex-col gap-2">
        {items.length === 0 && (
          <p className="text-foreground/50 text-sm">No events yet.</p>
        )}
        {items.map((item) => (
          <div
            key={item.id}
            className={`rounded-md border p-3 text-sm ${CATEGORY_STYLES[item.category]}`}
          >
            <div className="font-medium">{item.label}</div>
            <div className="text-foreground/50 text-xs">
              {new Date(item.timestamp).toLocaleTimeString()}
            </div>
          </div>
        ))}
      </div>
    </main>
  );
}
