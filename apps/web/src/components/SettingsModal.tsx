"use client";

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";
import {
  AgentsIcon,
  BillingIcon,
  CloseIcon,
  NotificationsIcon,
  SearchIcon,
  SecurityIcon,
  SessionsIcon,
  SettingsIcon,
  TeamIcon,
} from "./icons";
import { PROVIDER_LABELS, PROVIDER_LOGOS } from "./providerLogos";

interface SettingsUser {
  id: string;
  username: string;
  display_name?: string;
  preferred_name?: string;
  custom_instructions?: string;
  avatar_url?: string;
  email?: string;
}

interface Credential {
  id: string;
  provider: string;
  key_hint: string;
  verified_at?: string;
  created_at: string;
}

interface ProviderConfig {
  id: string;
  label: string;
}

// Same "not hardcoded to exactly two" note as the retired connect-agents
// page this list was ported from — add an entry, its card shows up, no
// other code changes. Labels are the product name (Claude, ChatGPT),
// not the company (Anthropic, OpenAI) — this screen is about connecting
// to the product this app talks to; `id` stays the company-scoped wire
// value the backend's credentials/provider resolution actually uses.
const PROVIDERS: ProviderConfig[] = [
  { id: "anthropic", label: PROVIDER_LABELS.anthropic },
  { id: "openai", label: PROVIDER_LABELS.openai },
];

interface SessionRow {
  id: string;
  user_agent?: string;
  ip_address?: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  revoked_at?: string;
  current: boolean;
}

export type SettingsCategory =
  | "general"
  | "agents"
  | "sessions"
  | "security"
  | "team"
  | "billing"
  | "notifications";

interface CategoryDef {
  key: SettingsCategory;
  label: string;
  Icon: React.ComponentType<{ size?: number }>;
  soon?: boolean;
}

const REAL_CATEGORIES: CategoryDef[] = [
  { key: "general", label: "General", Icon: SettingsIcon },
  { key: "agents", label: "Connected agents", Icon: AgentsIcon },
  { key: "sessions", label: "Sessions", Icon: SessionsIcon },
];

// Honest placeholders, not fake-functional — ADR-005. Each renders a
// plain, true explanation of why it isn't built rather than a dead form.
const PLACEHOLDER_CATEGORIES: CategoryDef[] = [
  { key: "security", label: "Security", Icon: SecurityIcon, soon: true },
  { key: "team", label: "Team & roles", Icon: TeamIcon, soon: true },
  { key: "billing", label: "Billing", Icon: BillingIcon, soon: true },
  {
    key: "notifications",
    label: "Notifications",
    Icon: NotificationsIcon,
    soon: true,
  },
];

const ALL_CATEGORIES = [...REAL_CATEGORIES, ...PLACEHOLDER_CATEGORIES];

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

// Best-effort, not a real UA-parsing library — this is display copy only
// ("Chrome on Windows"), never used for any security decision. Falls back
// to "Unknown browser"/"an unknown device" rather than guessing wrong.
function describeDevice(userAgent?: string): string {
  if (!userAgent) return "Unknown device";
  const browser = /Edg\//.test(userAgent)
    ? "Edge"
    : /Chrome\//.test(userAgent)
      ? "Chrome"
      : /Firefox\//.test(userAgent)
        ? "Firefox"
        : /Safari\//.test(userAgent)
          ? "Safari"
          : "Unknown browser";
  const os = /Windows/.test(userAgent)
    ? "Windows"
    : /iPhone/.test(userAgent)
      ? "iPhone"
      : /iPad/.test(userAgent)
        ? "iPad"
        : /Mac OS X/.test(userAgent)
          ? "macOS"
          : /Android/.test(userAgent)
            ? "Android"
            : /Linux/.test(userAgent)
              ? "Linux"
              : "an unknown device";
  return `${browser} on ${os}`;
}

function formatRelativeTime(iso: string): string {
  const diffMinutes = Math.floor((Date.now() - new Date(iso).getTime()) / 60000);
  if (diffMinutes < 1) return "just now";
  if (diffMinutes < 60) return `${diffMinutes}m ago`;
  const hours = Math.floor(diffMinutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export interface SettingsModalProps {
  open: boolean;
  onClose: () => void;
  initialCategory?: SettingsCategory;
  /** Called after a General-panel field successfully saves, so the
   * sidebar's own profile button (name/avatar) can refresh — it holds
   * its own separate /v1/users/me fetch and has no other way to learn
   * this changed. */
  onProfileSaved?: () => void;
}

/**
 * Account settings — ADR-005 / docs/design/settings-mockup.html. A modal,
 * not a page: the persistent shell (sidebar, current room) stays mounted
 * underneath. General, Connected agents, and Sessions are fully real;
 * Security, Team & roles, Billing, and Notifications are honest
 * placeholders with no backend calls behind them.
 */
export function SettingsModal({
  open,
  onClose,
  initialCategory,
  onProfileSaved,
}: SettingsModalProps) {
  const [category, setCategory] = useState<SettingsCategory>(
    initialCategory ?? "general",
  );
  const [search, setSearch] = useState("");

  useEffect(() => {
    // Resets to the requested (or default) category each time the modal
    // opens, so reopening it later doesn't leave it wherever it was left.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (open) setCategory(initialCategory ?? "general");
  }, [open, initialCategory]);

  useEffect(() => {
    if (!open) return;
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

  if (!open) return null;

  const query = search.trim().toLowerCase();
  const matches = (c: CategoryDef) => c.label.toLowerCase().includes(query);
  const visibleReal = REAL_CATEGORIES.filter(matches);
  const visiblePlaceholders = PLACEHOLDER_CATEGORIES.filter(matches);
  const activeDef = ALL_CATEGORIES.find((c) => c.key === category);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        onClick={(e) => e.stopPropagation()}
        className="flex h-[620px] max-h-[88vh] w-[900px] max-w-[92vw] overflow-hidden rounded-[14px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] shadow-[0_24px_64px_rgba(0,0,0,0.5)]"
      >
        <div className="flex w-[220px] shrink-0 flex-col overflow-y-auto border-r border-[var(--login-border)] bg-[var(--login-sidebar-bg)] p-3">
          <div className="mb-3.5 flex items-center gap-2 rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-2.5 py-2 text-[var(--login-text-muted)]">
            <SearchIcon />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search settings"
              className="w-full bg-transparent text-[13.5px] text-[var(--login-text)] outline-none placeholder:text-[var(--login-text-muted)]"
            />
          </div>

          {visibleReal.map((c) => (
            <CategoryButton
              key={c.key}
              def={c}
              active={category === c.key}
              onClick={() => setCategory(c.key)}
            />
          ))}

          {visibleReal.length > 0 && visiblePlaceholders.length > 0 && (
            <div className="mx-1.5 my-2.5 h-px bg-[var(--login-border)]" />
          )}

          {visiblePlaceholders.map((c) => (
            <CategoryButton
              key={c.key}
              def={c}
              active={category === c.key}
              onClick={() => setCategory(c.key)}
            />
          ))}

          {visibleReal.length === 0 && visiblePlaceholders.length === 0 && (
            <p className="px-2.5 py-2 text-[12.5px] text-[var(--login-text-muted)]">
              No settings match &quot;{search}&quot;.
            </p>
          )}
        </div>

        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex shrink-0 items-center justify-between border-b border-[var(--login-border)] px-6 py-4">
            <h2 className="text-[15px] font-semibold text-[var(--login-text)]">
              {activeDef?.label ?? ""}
            </h2>
            <button
              type="button"
              aria-label="Close settings"
              onClick={onClose}
              className="flex rounded-md p-1.5 text-[var(--login-text-muted)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
            >
              <CloseIcon />
            </button>
          </div>

          <div className="no-scrollbar flex-1 overflow-y-auto p-6">
            {category === "general" && (
              <GeneralPanel onSaved={onProfileSaved} />
            )}
            {category === "agents" && <ConnectedAgentsPanel />}
            {category === "sessions" && <SessionsPanel />}
            {category === "security" && (
              <PlaceholderPanel
                title="Security settings aren't built yet"
                body="Sessions above cover device access for now."
              />
            )}
            {category === "team" && (
              <PlaceholderPanel
                title="Team & roles aren't built yet"
                body="Harmonia is single-user for now — multi-person workspaces are a real, planned phase."
              />
            )}
            {category === "billing" && (
              <PlaceholderPanel
                title="Nothing to bill yet"
                body="You bring your own provider keys — Harmonia itself has no usage-based charges."
              />
            )}
            {category === "notifications" && (
              <PlaceholderPanel
                title="Notifications aren't built yet"
                body="Live updates currently only happen while a room is open."
              />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function CategoryButton({
  def,
  active,
  onClick,
}: {
  def: CategoryDef;
  active: boolean;
  onClick: () => void;
}) {
  const { Icon, label, soon } = def;
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13.5px] ${
        active
          ? "bg-[var(--login-surface-2)] text-[var(--login-text)]"
          : "text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
      }`}
    >
      <span className="opacity-85">
        <Icon />
      </span>
      <span className="flex-1">{label}</span>
      {soon && (
        <span className="font-[family-name:var(--login-font-mono)] text-[10px] text-[var(--login-text-muted)]">
          soon
        </span>
      )}
    </button>
  );
}

function PlaceholderPanel({ title, body }: { title: string; body: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 px-10 text-center text-[var(--login-text-muted)]">
      <div className="text-[14px] font-medium text-[var(--login-text-secondary)]">
        {title}
      </div>
      <p className="text-[13px] leading-[1.5]">{body}</p>
    </div>
  );
}

type FieldStatus = "idle" | "saving" | "saved" | "error";

function GeneralPanel({ onSaved }: { onSaved?: () => void }) {
  const [user, setUser] = useState<SettingsUser | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    // Own fetch, deliberately separate from the sidebar's — this panel
    // needs preferred_name/custom_instructions, which the sidebar's own
    // profile-button fetch has no reason to carry.
    apiFetch<SettingsUser>("/v1/users/me")
      .then(setUser)
      .catch((err: unknown) =>
        setLoadError(errorMessage(err, "Failed to load your profile.")),
      );
  }, []);

  if (loadError) {
    return <p className="text-[13.5px] text-red-400">{loadError}</p>;
  }
  if (!user) {
    return (
      <p className="text-[13.5px] text-[var(--login-text-muted)]">Loading…</p>
    );
  }

  const displayLabel = user.display_name || user.username;

  return (
    <div>
      <div className="mb-[22px] flex items-center gap-3.5">
        {user.avatar_url ? (
          // External OAuth-provider avatar URL, not a local asset
          // next/image can optimize without an allowlisted remote pattern.
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={user.avatar_url}
            alt=""
            className="h-[52px] w-[52px] rounded-full border border-[var(--login-border-strong)] object-cover"
          />
        ) : (
          <div className="flex h-[52px] w-[52px] items-center justify-center rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] text-[18px] font-semibold text-[var(--login-accent)]">
            {initials(displayLabel)}
          </div>
        )}
      </div>

      <SavableField
        label="Full name"
        field="display_name"
        initialValue={user.display_name ?? ""}
        onSaved={onSaved}
      />
      <SavableField
        label="What should agents call you?"
        field="preferred_name"
        initialValue={user.preferred_name ?? ""}
        onSaved={onSaved}
      />
      <SavableField
        label="Instructions for your agents"
        field="custom_instructions"
        initialValue={user.custom_instructions ?? ""}
        multiline
        placeholder="e.g. keep explanations brief, prefer Go over other languages when it's ambiguous"
        hint="Included every time an agent responds in any of your rooms."
        onSaved={onSaved}
      />
    </div>
  );
}

// Commit-on-blur, same pattern Sidebar's own room-rename input already
// uses — this modal's mockup has no explicit Save button, so each field
// saves itself the moment you leave it, only when its value actually
// changed.
function SavableField({
  label,
  field,
  initialValue,
  multiline,
  placeholder,
  hint,
  onSaved,
}: {
  label: string;
  field: "display_name" | "preferred_name" | "custom_instructions";
  initialValue: string;
  multiline?: boolean;
  placeholder?: string;
  hint?: string;
  onSaved?: () => void;
}) {
  const [value, setValue] = useState(initialValue);
  const savedValueRef = useRef(initialValue);
  const [status, setStatus] = useState<FieldStatus>("idle");
  const [error, setError] = useState<string | null>(null);

  const commit = async (raw: string) => {
    if (raw === savedValueRef.current) return;
    setStatus("saving");
    setError(null);
    try {
      await apiFetch("/v1/users/me", {
        method: "PATCH",
        body: { [field]: raw },
      });
      savedValueRef.current = raw;
      setStatus("saved");
      onSaved?.();
      setTimeout(() => setStatus((s) => (s === "saved" ? "idle" : s)), 1500);
    } catch (err) {
      setStatus("error");
      setError(errorMessage(err, "Failed to save."));
    }
  };

  const inputClassName =
    "w-full rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3 py-2 text-[13.5px] text-[var(--login-text)] outline-none focus:border-[var(--login-accent)]";

  return (
    <div className="mb-[22px]">
      <div className="mb-2 flex items-center gap-2 text-[13px] font-medium text-[var(--login-text)]">
        {label}
        {status === "saving" && (
          <span className="text-[11px] font-normal text-[var(--login-text-muted)]">
            Saving…
          </span>
        )}
        {status === "saved" && (
          <span className="text-[11px] font-normal text-[var(--login-accent)]">
            Saved
          </span>
        )}
      </div>
      {multiline ? (
        <textarea
          value={value}
          placeholder={placeholder}
          onChange={(e) => setValue(e.target.value)}
          onBlur={(e) => void commit(e.currentTarget.value)}
          className={`${inputClassName} min-h-[90px] resize-y leading-[1.5]`}
        />
      ) : (
        <input
          value={value}
          placeholder={placeholder}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur();
          }}
          onBlur={(e) => void commit(e.currentTarget.value)}
          className={inputClassName}
        />
      )}
      {hint && !error && (
        <p className="mt-1.5 text-[12px] leading-[1.5] text-[var(--login-text-muted)]">
          {hint}
        </p>
      )}
      {error && <p className="mt-1.5 text-[12px] text-red-400">{error}</p>}
    </div>
  );
}

function ConnectedAgentsPanel() {
  const [credentials, setCredentials] = useState<Credential[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const loadCredentials = useCallback(async () => {
    try {
      const data = await apiFetch<Credential[]>("/v1/credentials");
      setCredentials(data);
      setLoadError(null);
    } catch (err) {
      setCredentials([]);
      setLoadError(errorMessage(err, "Failed to load connected providers."));
    }
  }, []);

  useEffect(() => {
    // Fetch-on-mount, same pattern (and same justification) as the
    // retired connect-agents page this panel's logic was ported from.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadCredentials();
  }, [loadCredentials]);

  return (
    <div>
      {loadError && (
        <p className="mb-3 text-[13px] text-red-400">{loadError}</p>
      )}
      {PROVIDERS.map((provider) => (
        <ProviderRow
          key={provider.id}
          provider={provider}
          credential={
            credentials?.find((c) => c.provider === provider.id) ?? null
          }
          loading={credentials === null && !loadError}
          onChange={loadCredentials}
        />
      ))}
    </div>
  );
}

function ProviderRow({
  provider,
  credential,
  loading,
  onChange,
}: {
  provider: ProviderConfig;
  credential: Credential | null;
  loading: boolean;
  onChange: () => void;
}) {
  const [key, setKey] = useState("");
  const [connecting, setConnecting] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const connect = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setPending(true);
    setError(null);
    try {
      await apiFetch("/v1/credentials", {
        method: "POST",
        body: { provider: provider.id, key },
      });
      setKey("");
      setConnecting(false);
      onChange();
    } catch (err) {
      setError(errorMessage(err, "Failed to connect."));
    } finally {
      setPending(false);
    }
  };

  const disconnect = async () => {
    setPending(true);
    setError(null);
    try {
      await apiFetch(`/v1/credentials/${provider.id}`, { method: "DELETE" });
      onChange();
    } catch (err) {
      setError(errorMessage(err, "Failed to disconnect."));
    } finally {
      setPending(false);
    }
  };

  const ProviderLogo = PROVIDER_LOGOS[provider.id];

  return (
    <div className="mb-2.5 rounded-[10px] border border-[var(--login-border-strong)] p-3.5">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2.5">
          {ProviderLogo && (
            <span className="flex h-6 w-6 shrink-0 items-center justify-center text-[var(--login-text-secondary)]">
              <ProviderLogo size={18} />
            </span>
          )}
          <div>
            <div className="mb-0.5 text-[13.5px] font-medium text-[var(--login-text)]">
              {provider.label}
            </div>
            <div className="font-[family-name:var(--login-font-mono)] text-[12px] text-[var(--login-text-muted)]">
              {loading
                ? "Loading…"
                : credential
                  ? `Connected · •••${credential.key_hint}`
                  : "Not connected"}
            </div>
          </div>
        </div>
        {!loading &&
          (credential ? (
            <button
              type="button"
              onClick={() => void disconnect()}
              disabled={pending}
              className="rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3.5 py-1.5 text-[12.5px] text-[var(--login-text-secondary)] hover:border-[#F0705B] hover:text-[#F0705B] disabled:opacity-50"
            >
              {pending ? "Disconnecting…" : "Disconnect"}
            </button>
          ) : !connecting ? (
            <button
              type="button"
              onClick={() => setConnecting(true)}
              className="rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3.5 py-1.5 text-[12.5px] text-[var(--login-text-secondary)] hover:text-[var(--login-text)]"
            >
              Connect
            </button>
          ) : null)}
      </div>

      {error && <p className="mt-2 text-[12.5px] text-red-400">{error}</p>}

      {!loading && !credential && connecting && (
        <form onSubmit={connect} className="mt-3 flex gap-2">
          <input
            type="password"
            autoFocus
            required
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="API key"
            className="min-w-0 flex-1 rounded-lg border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-3 py-1.5 text-[13px] text-[var(--login-text)] outline-none focus:border-[var(--login-accent)]"
          />
          <button
            type="submit"
            disabled={pending || key === ""}
            className="rounded-lg bg-[var(--login-accent)] px-3.5 py-1.5 text-[12.5px] font-medium text-[var(--login-bg)] hover:bg-[#63e0d1] disabled:cursor-not-allowed disabled:opacity-50"
          >
            {pending ? "Connecting…" : "Save"}
          </button>
        </form>
      )}

      {!loading && !credential && connecting && (
        <p className="mt-2.5 text-[12px] leading-[1.5] text-[var(--login-text)]">
          Your key is encrypted before it&apos;s stored, decrypted only in
          memory at the moment we make a request to {provider.label} on your
          behalf, and never shown again or logged in plaintext after you
          enter it.
        </p>
      )}
    </div>
  );
}

function SessionsPanel() {
  const router = useRouter();
  const [sessions, setSessions] = useState<SessionRow[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [revokingId, setRevokingId] = useState<string | null>(null);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const loadSessions = useCallback(async () => {
    try {
      const data = await apiFetch<SessionRow[]>("/v1/sessions");
      setSessions(data);
      setLoadError(null);
    } catch (err) {
      setSessions([]);
      setLoadError(errorMessage(err, "Failed to load sessions."));
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadSessions();
  }, [loadSessions]);

  const revoke = async (session: SessionRow) => {
    setRevokingId(session.id);
    setRevokeError(null);
    try {
      await apiFetch(`/v1/sessions/${session.id}`, { method: "DELETE" });
      if (session.current) {
        // The backend already cleared the session cookie via this
        // response's own Set-Cookie header — going straight to /login
        // here is the same outcome useRequireAuth's 401 path reaches,
        // just without waiting for a doomed request to prove it.
        router.push("/login");
        return;
      }
      await loadSessions();
    } catch (err) {
      setRevokeError(errorMessage(err, "Failed to revoke session."));
    } finally {
      setRevokingId(null);
    }
  };

  if (loadError) {
    return <p className="text-[13.5px] text-red-400">{loadError}</p>;
  }
  if (sessions === null) {
    return (
      <p className="text-[13.5px] text-[var(--login-text-muted)]">
        Loading…
      </p>
    );
  }
  if (sessions.length === 0) {
    return (
      <p className="text-[13.5px] text-[var(--login-text-muted)]">
        No active sessions.
      </p>
    );
  }

  return (
    <div>
      {revokeError && (
        <p className="mb-3 text-[13px] text-red-400">{revokeError}</p>
      )}
      {sessions.map((s) => (
        <div
          key={s.id}
          className="flex items-center justify-between border-b border-[var(--login-border)] py-3 last:border-b-0"
        >
          <div>
            <div className="mb-0.5 flex items-center text-[13.5px] text-[var(--login-text)]">
              {describeDevice(s.user_agent)}
              {s.current && (
                <span className="ml-2 rounded-full border border-[var(--login-accent)]/35 px-2 py-[1px] font-[family-name:var(--login-font-mono)] text-[10.5px] text-[var(--login-accent)]">
                  This device
                </span>
              )}
            </div>
            <div className="font-[family-name:var(--login-font-mono)] text-[12px] text-[var(--login-text-muted)]">
              {s.current
                ? "Active now"
                : `Last active ${formatRelativeTime(s.last_seen_at)}`}
            </div>
          </div>
          <button
            type="button"
            onClick={() => void revoke(s)}
            disabled={revokingId === s.id}
            className="rounded-lg border border-[var(--login-border-strong)] bg-transparent px-3 py-1.5 text-[12.5px] text-[var(--login-text-secondary)] hover:border-[#F0705B] hover:text-[#F0705B] disabled:opacity-50"
          >
            {revokingId === s.id ? "Revoking…" : "Revoke"}
          </button>
        </div>
      ))}
    </div>
  );
}
