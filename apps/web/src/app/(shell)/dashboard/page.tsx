"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { ThinkingOrb } from "thinking-orbs";
import {
  AgentsIcon,
  HandoffIcon,
  PlusIcon,
  TeamIcon,
  WatchIcon,
} from "@/components/icons";
import { apiFetch } from "@/lib/api";

interface Me {
  display_name?: string;
  username: string;
}

function timeGreeting(name: string): string {
  const hour = new Date().getHours();
  const part = hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
  return `Good ${part}, ${name}`;
}

function headingOptions(name: string): string[] {
  return [
    timeGreeting(name),
    "What are we building?",
    "Where should we begin?",
    "What should we work on today?",
    "Ready when you are",
  ];
}

// Real destinations only — same "no page or endpoint for this" situation
// as the sidebar's Activity link (see docs/design/dashboard-build-brief.md).
const SUGGESTIONS = [
  { Icon: TeamIcon, label: "Have two agents review each other's work" },
  { Icon: HandoffIcon, label: "Hand off a task from one agent to another" },
  { Icon: WatchIcon, label: "Watch agents work together in real time" },
] as const;

export default function DashboardPage() {
  const [me, setMe] = useState<Me | null>(null);
  const [meLoaded, setMeLoaded] = useState(false);
  const [heading, setHeading] = useState<string | null>(null);

  useEffect(() => {
    void apiFetch<Me>("/v1/users/me")
      .then((data) => {
        setMe(data);
        setMeLoaded(true);
      })
      .catch(() => {
        setMe(null);
        setMeLoaded(true);
      });
  }, []);

  useEffect(() => {
    // Waits for the real name rather than picking (and possibly showing,
    // for the one option that uses it) a placeholder first — the mockup
    // never had to handle this since its name was a hardcoded string
    // with no fetch in between.
    if (!meLoaded) return;
    const name = me?.display_name || me?.username || "there";
    const options = headingOptions(name);
    const lastIndex = Number(sessionStorage.getItem("lastHeadingIndex"));
    let nextIndex = Math.floor(Math.random() * options.length);
    while (nextIndex === lastIndex && options.length > 1) {
      nextIndex = Math.floor(Math.random() * options.length);
    }
    sessionStorage.setItem("lastHeadingIndex", String(nextIndex));
    // Math.random() and sessionStorage are the actual side effects here —
    // this can't be computed synchronously during render (same reasoning
    // as connect-agents' own fetch-on-mount comment for this lint rule).
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setHeading(options[nextIndex]);
  }, [meLoaded, me]);

  return (
    <main className="flex h-full flex-col items-center justify-center p-10">
      <div className="max-w-[480px] text-center">
        <div className="mb-7 flex items-center justify-center gap-3.5">
          <ThinkingOrb
            state="working"
            size={64}
            theme="dark"
            style={{ width: 45, height: 45 }}
          />

          <h1 className="whitespace-nowrap text-[26px] font-medium tracking-[-0.01em] text-[var(--login-text)]">
            {heading ?? " "}
          </h1>
        </div>

        <div className="flex items-center justify-center gap-3">
          <Link
            href="/rooms/new"
            className="flex h-[46px] items-center gap-2 rounded-full bg-[var(--login-accent)] px-[22px] text-[14.5px] font-medium text-[var(--login-bg)] hover:bg-[#63e0d1]"
          >
            <PlusIcon size={15} strokeWidth={1.8} />
            Create a new room
          </Link>
          <Link
            href="/connect-agents"
            className="flex h-[46px] items-center gap-2 rounded-full border border-[var(--login-border-strong)] bg-[var(--login-surface-2)] px-[22px] text-[14.5px] font-medium text-[var(--login-text)] hover:border-[#3A4453] hover:bg-[#1C222B]"
          >
            <AgentsIcon />
            Connect an agent
          </Link>
        </div>

        <div className="mt-8 flex flex-col gap-0.5 text-left">
          {SUGGESTIONS.map(({ Icon, label }) => (
            <a
              key={label}
              href="#"
              className="flex items-center gap-2.5 rounded-lg px-3 py-2.5 text-[13.5px] text-[var(--login-text-secondary)] hover:bg-[var(--login-surface-2)] hover:text-[var(--login-text)]"
            >
              <Icon />
              {label}
            </a>
          ))}
        </div>
      </div>
    </main>
  );
}
