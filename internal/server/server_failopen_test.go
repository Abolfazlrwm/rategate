package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/algorithms"
)

// errStore is a Store stand-in whose Check always errors, used to exercise the
// fail-open / fail-closed branches of Server.Check. It satisfies store.Store
// structurally (Check / Name / Close).
type errStore struct{}

func (errStore) Check(_ context.Context, _, _ string, _ algorithms.Policy, now time.Time) (algorithms.Decision, error) {
	return algorithms.Decision{Allowed: false, ResetAt: now}, errors.New("store unavailable")
}

func (errStore) Name() string { return "err" }
func (errStore) Close() error { return nil }

// TestServer_CheckFailClosedDefault asserts the default (store.fail_open
// false) surfaces the store error and does not admit — the safe choice.
func TestServer_CheckFailClosedDefault(t *testing.T) {
	core, _ := newTestServer(t)
	core.store = errStore{} // store always errors; FailOpen defaults to false

	res, err := core.Check(context.Background(), CheckRequest{
		Key:       "k",
		Algorithm: "tokenbucket",
		Policy:    algorithms.Policy{Limit: 1, RefillPerSecond: 1},
	}, time.Now())
	if err == nil {
		t.Fatal("want error on store failure (fail-closed default)")
	}
	if res.Allowed {
		t.Fatal("fail-closed must not admit")
	}
}

// TestServer_CheckFailOpen asserts that with store.fail_open=true a store
// error is swallowed and the request is admitted (availability over safety),
// with no error surfaced to the transport.
func TestServer_CheckFailOpen(t *testing.T) {
	core, _ := newTestServer(t)
	core.store = errStore{}
	core.cfg.Store.FailOpen = true

	res, err := core.Check(context.Background(), CheckRequest{
		Key:       "k",
		Algorithm: "tokenbucket",
		Policy:    algorithms.Policy{Limit: 1, RefillPerSecond: 1},
	}, time.Now())
	if err != nil {
		t.Fatalf("fail-open must not surface error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("fail-open must admit on store failure")
	}
}
