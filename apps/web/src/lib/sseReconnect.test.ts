import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ReconnectingEventSource,
  SSE_CLOSED,
  type EventSourceLike,
} from "./sseReconnect";

const CONNECTING = 0;
const OPEN = 1;

// A minimal fake satisfying EventSourceLike — no jsdom/real EventSource
// needed, since ReconnectingEventSource only ever touches readyState,
// close(), and addEventListener().
class FakeEventSource implements EventSourceLike {
  readyState = CONNECTING;
  closed = false;
  private listeners: Record<string, Array<() => void>> = {};

  addEventListener(type: string, listener: () => void) {
    (this.listeners[type] ??= []).push(listener);
  }

  close() {
    this.closed = true;
  }

  private emit(type: string) {
    for (const listener of this.listeners[type] ?? []) listener();
  }

  /** A real successful connection. */
  open() {
    this.readyState = OPEN;
    this.emit("open");
  }

  /** The real failure this module exists for: a non-2xx response the
   *  browser will never retry on its own. */
  failPermanently() {
    this.readyState = SSE_CLOSED;
    this.emit("error");
  }

  /** An ordinary transient drop mid-retry — EventSource handles this
   *  itself; readyState is CONNECTING, not CLOSED, while it does. */
  failTransiently() {
    this.readyState = CONNECTING;
    this.emit("error");
  }
}

function fakeFactory() {
  const sources: FakeEventSource[] = [];
  const create = vi.fn(() => {
    const source = new FakeEventSource();
    sources.push(source);
    return source;
  });
  return { create, sources };
}

describe("ReconnectingEventSource", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("reconnects after a forced non-2xx failure and recovers", () => {
    const { create, sources } = fakeFactory();
    const reconnecting = new ReconnectingEventSource({
      create,
      baseDelayMs: 1000,
      maxDelayMs: 8000,
    });

    expect(create).toHaveBeenCalledTimes(1);

    // Force exactly the failure EventSource itself never retries.
    sources[0].failPermanently();

    // No reconnect before the backoff delay elapses.
    vi.advanceTimersByTime(999);
    expect(create).toHaveBeenCalledTimes(1);

    // The real reconnect attempt: a brand-new EventSource.
    vi.advanceTimersByTime(1);
    expect(create).toHaveBeenCalledTimes(2);
    expect(reconnecting.source).toBe(sources[1]);

    // It succeeds — the connection has genuinely recovered.
    sources[1].open();
    expect(reconnecting.source).toBe(sources[1]);

    reconnecting.close();
  });

  it("backs off exponentially on repeated failures, capped at maxDelayMs", () => {
    const { create, sources } = fakeFactory();
    new ReconnectingEventSource({
      create,
      baseDelayMs: 1000,
      maxDelayMs: 3000,
    });

    sources[0].failPermanently();
    vi.advanceTimersByTime(1000);
    expect(create).toHaveBeenCalledTimes(2);

    sources[1].failPermanently();
    vi.advanceTimersByTime(1999);
    expect(create).toHaveBeenCalledTimes(2);
    vi.advanceTimersByTime(1);
    expect(create).toHaveBeenCalledTimes(3);

    sources[2].failPermanently();
    // Would be 4000ms uncapped — proves the real cap, not just the doubling.
    vi.advanceTimersByTime(2999);
    expect(create).toHaveBeenCalledTimes(3);
    vi.advanceTimersByTime(1);
    expect(create).toHaveBeenCalledTimes(4);
  });

  it("resets the backoff delay after a real successful reconnect", () => {
    const { create, sources } = fakeFactory();
    new ReconnectingEventSource({
      create,
      baseDelayMs: 1000,
      maxDelayMs: 8000,
    });

    sources[0].failPermanently();
    vi.advanceTimersByTime(1000);
    expect(create).toHaveBeenCalledTimes(2);
    sources[1].open();

    sources[1].failPermanently();
    // If the delay had stayed doubled at 2000ms this would be too early —
    // a real recovery must reset it back to baseDelayMs.
    vi.advanceTimersByTime(1000);
    expect(create).toHaveBeenCalledTimes(3);
  });

  it("leaves an ordinary transient drop to EventSource's own native retry", () => {
    const { create, sources } = fakeFactory();
    new ReconnectingEventSource({ create });

    sources[0].failTransiently();

    vi.advanceTimersByTime(60000);
    expect(create).toHaveBeenCalledTimes(1);
  });

  it("cancels a pending reconnect for good once closed", () => {
    const { create, sources } = fakeFactory();
    const reconnecting = new ReconnectingEventSource({
      create,
      baseDelayMs: 1000,
    });

    // A permanent failure already leaves nothing live to close (that's
    // what CLOSED means) — the real thing this proves is that the
    // reconnect attempt it scheduled never fires once closed.
    sources[0].failPermanently();
    reconnecting.close();

    vi.advanceTimersByTime(60000);
    expect(create).toHaveBeenCalledTimes(1);
    expect(reconnecting.source).toBeNull();
  });

  it("closes the currently active connection when closed", () => {
    const { create, sources } = fakeFactory();
    const reconnecting = new ReconnectingEventSource({
      create,
      baseDelayMs: 1000,
    });

    sources[0].open();
    reconnecting.close();

    expect(sources[0].closed).toBe(true);
    expect(reconnecting.source).toBeNull();
  });
});
