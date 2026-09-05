"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { apiFetch, ApiError } from "@/lib/api";

interface Room {
  id: string;
  name: string;
  status: string;
  created_at: string;
}

export default function NewRoomPage() {
  const router = useRouter();
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setPending(true);
    setError(null);
    try {
      const room = await apiFetch<Room>("/v1/rooms", {
        method: "POST",
        body: { name },
      });
      router.push(`/rooms/${room.id}`);
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Failed to create room.",
      );
      setPending(false);
    }
  };

  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-6 p-8 text-center">
      <h1 className="text-2xl font-semibold">Create a room</h1>
      <form onSubmit={submit} className="flex w-full max-w-sm flex-col gap-3">
        <input
          type="text"
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Room name"
          className="rounded-md border border-foreground/20 px-3 py-2 text-sm"
        />
        <button
          type="submit"
          disabled={pending || name === ""}
          className="rounded-md border border-foreground/20 px-4 py-2 font-medium transition-colors hover:bg-foreground/5 disabled:opacity-50"
        >
          {pending ? "Creating…" : "Create room"}
        </button>
        {error && <p className="text-sm text-red-500">{error}</p>}
      </form>
    </main>
  );
}
