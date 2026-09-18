package cost

import "testing"

func TestEstimateUSD_KnownProviders(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		inputTokens  int
		outputTokens int
		want         float64
	}{
		{"anthropic", "anthropic", 1_000_000, 1_000_000, 18},
		{"openai", "openai", 1_000_000, 1_000_000, 0.75},
		{"anthropic partial", "anthropic", 100_000, 50_000, 0.3 + 0.75},
		{"unknown provider", "cohere", 1_000_000, 1_000_000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateUSD(tt.provider, tt.inputTokens, tt.outputTokens)
			if got != tt.want {
				t.Errorf("EstimateUSD(%q, %d, %d) = %v, want %v", tt.provider, tt.inputTokens, tt.outputTokens, got, tt.want)
			}
		})
	}
}
