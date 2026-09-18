package agentloop

import (
	"testing"
	"time"
)

func TestBounds_Validate(t *testing.T) {
	tests := []struct {
		name    string
		bounds  Bounds
		wantErr bool
	}{
		{"all set", Bounds{MaxCycles: 10, DollarCapUSD: 1, WallClock: time.Minute}, false},
		{"zero max cycles", Bounds{MaxCycles: 0, DollarCapUSD: 1, WallClock: time.Minute}, true},
		{"negative max cycles", Bounds{MaxCycles: -1, DollarCapUSD: 1, WallClock: time.Minute}, true},
		{"zero dollar cap", Bounds{MaxCycles: 10, DollarCapUSD: 0, WallClock: time.Minute}, true},
		{"negative dollar cap", Bounds{MaxCycles: 10, DollarCapUSD: -1, WallClock: time.Minute}, true},
		{"zero wall clock", Bounds{MaxCycles: 10, DollarCapUSD: 1, WallClock: 0}, true},
		{"negative wall clock", Bounds{MaxCycles: 10, DollarCapUSD: 1, WallClock: -time.Second}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bounds.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
