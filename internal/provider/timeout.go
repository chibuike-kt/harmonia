package provider

import (
	"context"
	"fmt"
	"time"
)

// RequestTimeout bounds one real call to an Agent's Generate — every
// live network round trip this package's own clients ever make,
// including a classification-only call (ADR-007 batch B's phase 1) or a
// forced-search call (ADR-008 batch B), not just an ordinary reply.
//
// Derived from this project's own live testing, not picked as a round
// number: across a real dogfooding pass and this project's own P1 live
// verification (both against real OpenAI traffic, including deliberately
// heavy multi-section design prompts run to try to stress it), the
// slowest real generation observed — an 878-output-token reply covering
// eight separate design questions with full reasoning for each — still
// completed in 7.6 seconds. 45s gives roughly 6x headroom over that
// worst real case, while staying well under message.generationTimeout's
// 2-minute whole-invocation budget, leaving real slack for the
// surrounding work (agent lookup, credential resolution, history load)
// and, critically, for the recovery path itself to run once this fires.
const RequestTimeout = 45 * time.Second

// CallWithTimeout runs client.Generate bounded by timeout, in its own
// goroutine, racing it against a derived context rather than trusting
// Generate's own implementation to honor ctx cancellation. Every real
// client in this codebase does wire ctx into its outbound HTTP request
// (see openai.Client and anthropic.Client), so in the ordinary case this
// timeout and that request-level cancellation fire together — but a
// caller has no way to verify that of every Agent it might ever be
// handed (this package's own tests substitute fakes directly), and nothing
// stops a future implementation from getting that wrong. This is the one
// place that protects every caller regardless: a client that ignores ctx
// entirely — blocks on a stalled connection, a deadlock, a bug — still
// can't hang its caller past timeout.
//
// The blocked goroutine itself is abandoned, not killed, when timeout
// wins the race — Go has no mechanism to force-stop a running goroutine,
// and client.Generate may still complete or leak in the background. That
// leak is the accepted cost of bounding a call that refuses to be
// canceled; the alternative (an unbounded wait) is strictly worse — see
// this project's own dogfooding notes for a real instance of that
// unbounded wait leaving an agent stuck showing "running" indefinitely.
func CallWithTimeout(ctx context.Context, timeout time.Duration, client Agent, req GenerateRequest) (GenerateResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type result struct {
		resp     GenerateResponse
		err      error
		panicVal any
	}
	ch := make(chan result, 1)
	go func() {
		// A panic inside client.Generate happens on this goroutine, not
		// the caller's — left uncaught, it would crash the whole process
		// rather than being handled by whatever recover() the caller
		// already wraps its own invocation in (every real caller in this
		// codebase has one). Recovered here and re-panicked below, on the
		// caller's own goroutine, once CallWithTimeout's select picks it
		// up — restoring the exact same panic-recovery semantics a
		// direct, unwrapped client.Generate(ctx, req) call would have
		// had.
		defer func() {
			if r := recover(); r != nil {
				ch <- result{panicVal: r}
			}
		}()
		resp, err := client.Generate(ctx, req)
		ch <- result{resp: resp, err: err}
	}()

	select {
	case r := <-ch:
		if r.panicVal != nil {
			panic(r.panicVal)
		}
		return r.resp, r.err
	case <-ctx.Done():
		return GenerateResponse{}, fmt.Errorf("provider: request timed out after %s", timeout)
	}
}
