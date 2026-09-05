"use client";

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { apiFetch, ApiError } from "@/lib/api";

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

// Not hardcoded to exactly two: add an entry here and its card shows up
// with no other code changes, per the product discussion about staying
// open to more providers than Anthropic/OpenAI.
const PROVIDERS: ProviderConfig[] = [
  { id: "anthropic", label: "Anthropic" },
  { id: "openai", label: "OpenAI" },
];

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function ConnectAgentsPage() {
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
    // There's no framework-level fetch to reach for here: auth is a
    // same-origin browser cookie, so loading this from a Server
    // Component would mean manually forwarding it cross-origin to the
    // Go backend. A plain fetch-on-mount is the standard pattern for
    // exactly this case (see react.dev's own "fetching data" example) —
    // the same callback is reused as onChange after a connect/disconnect,
    // which isn't in an effect at all and doesn't trip this rule there.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadCredentials();
  }, [loadCredentials]);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-1 flex-col gap-6 p-8">
      <h1 className="text-center text-2xl font-semibold">Connect agents</h1>
      {loadError && (
        <p className="text-center text-sm text-red-500">{loadError}</p>
      )}
      <div className="flex flex-col gap-4">
        {PROVIDERS.map((provider) => (
          <ProviderCard
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
    </main>
  );
}

function ProviderCard({
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
      onChange();
    } catch (err) {
      // Reflects Connect's own verify-before-save result — a rejected
      // key surfaces exactly the message the backend gave, not a
      // client-side re-derivation of what "valid" means.
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

  return (
    <div className="rounded-md border border-foreground/20 p-4">
      <div className="flex items-center justify-between gap-4">
        <h2 className="font-medium">{provider.label}</h2>
        {loading ? (
          <span className="text-foreground/50 text-sm">Loading…</span>
        ) : credential ? (
          <span className="text-sm text-green-600 dark:text-green-400">
            Connected · •••{credential.key_hint}
          </span>
        ) : (
          <span className="text-foreground/50 text-sm">Not connected</span>
        )}
      </div>

      {error && <p className="mt-2 text-sm text-red-500">{error}</p>}

      {!loading &&
        (credential ? (
          <button
            type="button"
            onClick={disconnect}
            disabled={pending}
            className="mt-3 rounded-md border border-foreground/20 px-3 py-1.5 text-sm transition-colors hover:bg-foreground/5 disabled:opacity-50"
          >
            {pending ? "Disconnecting…" : "Disconnect"}
          </button>
        ) : (
          <form onSubmit={connect} className="mt-3 flex gap-2">
            <input
              type="password"
              required
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="API key"
              className="min-w-0 flex-1 rounded-md border border-foreground/20 px-3 py-1.5 text-sm"
            />
            <button
              type="submit"
              disabled={pending || key === ""}
              className="rounded-md border border-foreground/20 px-3 py-1.5 text-sm transition-colors hover:bg-foreground/5 disabled:opacity-50"
            >
              {pending ? "Connecting…" : "Connect"}
            </button>
          </form>
        ))}
    </div>
  );
}
