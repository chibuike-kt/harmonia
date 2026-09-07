"use client";

import { useEffect, useRef, useState } from "react";
import { apiFetch, ApiError } from "@/lib/api";
import { PlusIcon } from "./icons";
import { PROVIDER_LABELS, PROVIDER_LOGOS } from "./providerLogos";

interface Credential {
  id: string;
  provider: string;
}

export interface AddedAgent {
  id: string;
  name: string;
  provider: string;
  capabilities: string[];
}

interface AddAgentMenuProps {
  roomId: string;
  onAdded: (agent: AddedAgent) => void;
  /** Icon-only (the room header's compact chip row) when omitted; shows
   *  this text alongside the icon when given (the info panel's own
   *  section-header action). */
  label?: string;
}

/**
 * "+ Add agent" — the real fix for a real gap: POST
 * /v1/rooms/{room_id}/agents has existed since early in this build, but
 * nothing in the frontend ever called it outside test tooling. A user
 * could connect a BYOK credential and still have zero agents in every
 * room. This queries the user's own connected credentials and turns
 * each into a one-click "add" option — selecting one registers a new
 * agent with a sensible default name (the provider's product name) and
 * that provider, live and @mentionable immediately via the same
 * onAdded → room page state refresh every other room mutation here uses.
 * Zero connected credentials gets a plain, honest message and a link
 * into Settings' Connected agents category — not a silently empty list.
 */
export function AddAgentMenu({ roomId, onAdded, label }: AddAgentMenuProps) {
  const [open, setOpen] = useState(false);
  const [credentials, setCredentials] = useState<Credential[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [addingProvider, setAddingProvider] = useState<string | null>(null);
  const [addError, setAddError] = useState<string | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onDocumentClick(event: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
  }, []);

  const loadCredentials = () => {
    void apiFetch<Credential[]>("/v1/credentials")
      .then((data) => {
        setCredentials(data);
        setLoadError(null);
      })
      .catch((err: unknown) => {
        setCredentials([]);
        setLoadError(
          err instanceof ApiError
            ? err.message
            : "Failed to load connected providers.",
        );
      });
  };

  const handleToggle = () => {
    setOpen((wasOpen) => {
      const nextOpen = !wasOpen;
      if (nextOpen && credentials === null) loadCredentials();
      return nextOpen;
    });
  };

  const addAgent = async (provider: string) => {
    setAddingProvider(provider);
    setAddError(null);
    try {
      const agent = await apiFetch<AddedAgent>(`/v1/rooms/${roomId}/agents`, {
        method: "POST",
        body: {
          name: PROVIDER_LABELS[provider] ?? provider,
          provider,
          capabilities: [],
        },
      });
      onAdded(agent);
      setOpen(false);
    } catch (err) {
      setAddError(
        err instanceof ApiError ? err.message : "Failed to add agent.",
      );
    } finally {
      setAddingProvider(null);
    }
  };

  return (
    <div ref={menuRef} className="relative">
      <button
        type="button"
        title="Add agent"
        onClick={handleToggle}
        className={
          label
            ? "flex items-center gap-1.5 rounded-full border border-dashed border-[var(--login-border-strong)] px-2.5 py-1 text-[12px] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
            : "flex h-6 w-6 items-center justify-center rounded-full border border-dashed border-[var(--login-border-strong)] text-[var(--login-text-secondary)] hover:border-[var(--login-accent)] hover:text-[var(--login-text)]"
        }
      >
        <PlusIcon size={12} strokeWidth={1.8} />
        {label}
      </button>

      {open && (
        <div className="absolute left-0 top-full z-10 mt-1.5 w-[260px] rounded-[10px] border border-[var(--login-border-strong)] bg-[var(--login-surface)] p-1.5 shadow-[0_8px_24px_rgba(0,0,0,0.4)]">
          {credentials === null ? (
            <p className="px-2 py-2 text-[12.5px] text-[var(--login-text-muted)]">
              Loading…
            </p>
          ) : loadError ? (
            <p className="px-2 py-2 text-[12.5px] text-red-400">{loadError}</p>
          ) : credentials.length === 0 ? (
            <div className="px-2 py-2">
              <p className="mb-2 text-[12.5px] leading-[1.5] text-[var(--login-text)]">
                No providers connected yet — connect one to add an agent.
              </p>
              <button
                type="button"
                onClick={() => {
                  setOpen(false);
                  window.dispatchEvent(
                    new CustomEvent("harmonia:open-settings", {
                      detail: { category: "agents" },
                    }),
                  );
                }}
                className="text-[12.5px] text-[var(--login-accent)] underline hover:text-[#63e0d1]"
              >
                Open Connected agents settings
              </button>
            </div>
          ) : (
            credentials.map((c) => {
              const Logo = PROVIDER_LOGOS[c.provider];
              const busy = addingProvider === c.provider;
              return (
                <button
                  key={c.id}
                  type="button"
                  disabled={busy}
                  onClick={() => void addAgent(c.provider)}
                  className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] text-[var(--login-text)] hover:bg-[var(--login-surface-2)] disabled:opacity-60"
                >
                  {Logo && <Logo size={16} />}
                  {PROVIDER_LABELS[c.provider] ?? c.provider}
                  {busy && (
                    <span className="ml-auto text-[11px] text-[var(--login-text-muted)]">
                      Adding…
                    </span>
                  )}
                </button>
              );
            })
          )}
          {addError && (
            <p className="px-2 pt-1 text-[12px] text-red-400">{addError}</p>
          )}
        </div>
      )}
    </div>
  );
}
