// Per-million-token list prices, used only to turn real captured token
// counts (internal/message.Message.InputTokens/OutputTokens, carried
// over the wire on every ChatMessage) into an approximate dollar figure
// for the room header's cost pill.
//
// NOT AUTHORITATIVE. This is a small hardcoded table, not a connection
// to either provider's real billing API — it will silently drift out of
// date as prices change, and it's keyed by provider because this
// codebase's own provider clients (internal/provider/anthropic,
// internal/provider/openai) each hardcode a single fixed default model
// with no per-agent choice, not because pricing is actually uniform
// across every model a provider offers. Treat the pill as a rough,
// directional estimate for a dev/BYOK context, never as a bill — for an
// authoritative number, check the provider's own usage dashboard.
const PRICE_PER_MILLION_TOKENS: Record<
  string,
  { input: number; output: number }
> = {
  // Tied to internal/provider/anthropic's defaultModel ("claude-sonnet-5").
  anthropic: { input: 3, output: 15 },
  // Tied to internal/provider/openai's defaultModel ("gpt-4o-mini").
  openai: { input: 0.15, output: 0.6 },
};

export function estimateCostUSD(
  provider: string,
  inputTokens: number,
  outputTokens: number,
): number {
  const price = PRICE_PER_MILLION_TOKENS[provider];
  if (!price) return 0;
  return (inputTokens * price.input + outputTokens * price.output) / 1_000_000;
}

export function formatTokenCount(count: number): string {
  if (count < 1000) return String(count);
  return `${(count / 1000).toFixed(1)}k`;
}

// A cheap model on a short exchange genuinely costs a few thousandths
// of a cent — real math, not a bug — but toFixed(3) rounds that to
// "$0.000", which reads as "free" rather than "small." Scaling decimal
// places to the actual magnitude keeps the figure honest instead of
// hiding it behind fixed precision that happens to suit larger totals.
export function formatCostUSD(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return `$${usd.toFixed(5)}`;
  return `$${usd.toFixed(2)}`;
}
