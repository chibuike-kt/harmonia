// EventSource already retries an ordinary transient drop on its own —
// a live connection that goes from open to disconnected reconnects
// using its own internal timer, no help needed. The one real gap: per
// spec, if even the very first response comes back with anything but a
// 2xx (a transient 500, a server not warmed up yet), the browser "fails
// the connection" permanently — readyState sticks at CLOSED and it
// never tries again on its own. ReconnectingEventSource exists only to
// cover that one gap, with real exponential backoff, capped and reset
// the instant a connection actually opens.
//
// EventSourceLike is the minimal real EventSource surface this needs —
// the real DOM EventSource satisfies it as-is; a test's fake only has
// to implement this much, no DOM/EventSource polyfill required.
export interface EventSourceLike {
  readyState: number;
  close(): void;
  addEventListener(type: string, listener: () => void): void;
}

// Matches the real EventSource.CLOSED value (2) — duplicated here
// rather than referencing the DOM global so this module has no DOM
// dependency at all and can be unit-tested in a plain node environment.
export const SSE_CLOSED = 2;

export interface ReconnectingEventSourceOptions {
  /** Builds and returns a new real EventSource (or test fake) each call. */
  create: () => EventSourceLike;
  baseDelayMs?: number;
  maxDelayMs?: number;
}

export class ReconnectingEventSource {
  private current: EventSourceLike | null = null;
  private cancelled = false;
  private delayMs: number;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private readonly baseDelayMs: number;
  private readonly maxDelayMs: number;
  private readonly createSource: () => EventSourceLike;

  constructor({
    create,
    baseDelayMs = 1000,
    maxDelayMs = 30000,
  }: ReconnectingEventSourceOptions) {
    this.createSource = create;
    this.baseDelayMs = baseDelayMs;
    this.maxDelayMs = maxDelayMs;
    this.delayMs = baseDelayMs;
    this.connect();
  }

  /** The real EventSource currently in use, if any — null while a reconnect is pending. */
  get source(): EventSourceLike | null {
    return this.current;
  }

  private connect() {
    if (this.cancelled) return;
    const source = this.createSource();
    this.current = source;

    source.addEventListener("open", () => {
      // A real, successful connection — the backoff a prior failure may
      // have built up no longer reflects anything real.
      this.delayMs = this.baseDelayMs;
    });

    source.addEventListener("error", () => {
      // CONNECTING (or OPEN, mid-retry) here means the browser's own
      // native retry is already in flight — stepping in too would just
      // race it with a second, redundant connection.
      if (source.readyState !== SSE_CLOSED) return;
      if (this.current === source) this.current = null;
      this.scheduleReconnect();
    });
  }

  private scheduleReconnect() {
    if (this.cancelled) return;
    const delay = this.delayMs;
    this.delayMs = Math.min(this.delayMs * 2, this.maxDelayMs);
    this.timer = setTimeout(() => {
      this.timer = null;
      this.connect();
    }, delay);
  }

  /** Stops reconnecting for good and closes whatever real connection is open. */
  close() {
    this.cancelled = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.current?.close();
    this.current = null;
  }
}
