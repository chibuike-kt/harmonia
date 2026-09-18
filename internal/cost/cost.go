// Package cost is the Go-side twin of apps/web/src/lib/tokenPricing.ts —
// the same small, hardcoded per-million-token price table, kept in sync
// by hand rather than shared, since one lives in a Next.js bundle and the
// other in this binary. Before ADR-011 this estimate only ever mattered
// for the room header's cost pill (a display concern, wrong-by-a-cent
// tolerable); the agent loop's dollar cap (ADR-011) is the first real
// caller that *enforces* against this number, which is why a server-side
// equivalent exists at all now.
//
// NOT AUTHORITATIVE, same caveat as the frontend table: not a connection
// to either provider's real billing API, and keyed by provider only
// because this codebase's own provider clients each hardcode a single
// default model. Treat it as a rough, directional estimate.
package cost

// pricePerMillionTokens mirrors tokenPricing.ts's PRICE_PER_MILLION_TOKENS
// exactly — tied to internal/provider/anthropic's defaultModel
// ("claude-sonnet-5") and internal/provider/openai's defaultModel
// ("gpt-4o-mini"). Update both tables together if either changes.
var pricePerMillionTokens = map[string]struct{ Input, Output float64 }{
	"anthropic": {Input: 3, Output: 15},
	"openai":    {Input: 0.15, Output: 0.6},
}

// EstimateUSD returns providerName's estimated real dollar cost for
// inputTokens/outputTokens — 0 for a provider not in the table, the same
// silent-zero fallback estimateCostUSD uses rather than an error, since a
// display/cap estimate degrading to "no charge" for an unrecognized
// provider is safer than either panicking or blocking a real session.
func EstimateUSD(providerName string, inputTokens, outputTokens int) float64 {
	price, ok := pricePerMillionTokens[providerName]
	if !ok {
		return 0
	}
	return (float64(inputTokens)*price.Input + float64(outputTokens)*price.Output) / 1_000_000
}
