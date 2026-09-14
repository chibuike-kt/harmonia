package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

// blockingAgent never returns from Generate on its own — no select on
// ctx.Done(), no timer, nothing. It exists to prove CallWithTimeout
// bounds a call regardless of whether the Agent itself cooperates with
// context cancellation, the worst real case this wrapper exists for.
type blockingAgent struct{}

func (blockingAgent) Generate(context.Context, GenerateRequest) (GenerateResponse, error) {
	select {}
}

func TestCallWithTimeout_BoundsAHungAgent(t *testing.T) {
	start := time.Now()
	const budget = 50 * time.Millisecond

	_, err := CallWithTimeout(context.Background(), budget, blockingAgent{}, GenerateRequest{})

	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("err = nil, want a timeout error")
	}
	// Generous margin over budget — this only needs to prove the call
	// returned in bounded time, not that it returned at the exact
	// instant the deadline fired.
	if elapsed > budget+2*time.Second {
		t.Fatalf("CallWithTimeout took %s against a %s budget, want it bounded near the budget", elapsed, budget)
	}
}

// slowButFiniteAgent respects ctx like every real client in this
// codebase does — it exists to prove CallWithTimeout doesn't fire early
// against a call that's merely slow, not actually hung.
type slowButFiniteAgent struct {
	delay time.Duration
	resp  GenerateResponse
}

func (a slowButFiniteAgent) Generate(ctx context.Context, _ GenerateRequest) (GenerateResponse, error) {
	select {
	case <-time.After(a.delay):
		return a.resp, nil
	case <-ctx.Done():
		return GenerateResponse{}, ctx.Err()
	}
}

func TestCallWithTimeout_ReturnsARealFastResponseUnaltered(t *testing.T) {
	want := GenerateResponse{Content: "real reply"}
	resp, err := CallWithTimeout(context.Background(), time.Second, slowButFiniteAgent{delay: 10 * time.Millisecond, resp: want}, GenerateRequest{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.Content != want.Content {
		t.Fatalf("resp.Content = %q, want %q", resp.Content, want.Content)
	}
}

// erroringAgent returns immediately with a real provider error — the
// ordinary "the API call itself failed" case, distinct from both a
// timeout and a real success.
type erroringAgent struct{ err error }

func (a erroringAgent) Generate(context.Context, GenerateRequest) (GenerateResponse, error) {
	return GenerateResponse{}, a.err
}

func TestCallWithTimeout_PropagatesARealProviderError(t *testing.T) {
	wantErr := errors.New("openai: 429 rate limited")
	_, err := CallWithTimeout(context.Background(), time.Second, erroringAgent{err: wantErr}, GenerateRequest{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// panickingAgent panics instead of returning — proves a panic inside
// Generate propagates to CallWithTimeout's own caller (recoverable
// there, the same as an unwrapped client.Generate call would have been)
// rather than crashing the process from the unrecovered goroutine
// CallWithTimeout runs Generate on internally.
type panickingAgent struct{}

func (panickingAgent) Generate(context.Context, GenerateRequest) (GenerateResponse, error) {
	panic("panickingAgent: simulated panic")
}

func TestCallWithTimeout_PropagatesAPanicToTheCaller(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("recover() = nil, want CallWithTimeout's panic to reach this goroutine's own recover()")
		}
		if r != "panickingAgent: simulated panic" {
			t.Fatalf("recovered value = %v, want the original panic value preserved", r)
		}
	}()
	_, _ = CallWithTimeout(context.Background(), time.Second, panickingAgent{}, GenerateRequest{})
	t.Fatal("unreachable: CallWithTimeout should have panicked")
}
