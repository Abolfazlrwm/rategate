package admin

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Abolfazlrwm/rategate/internal/config"
)

func seedPolicies() map[string]config.PolicyConfig {
	return map[string]config.PolicyConfig{
		"strict": {Name: "strict", Algorithm: "tokenbucket", Limit: 2, RefillPerSecond: 1},
		"sw":     {Name: "sw", Algorithm: "slidingwindow", Limit: 5, Window: time.Minute},
	}
}

func TestRegistry_GetHitAndMiss(t *testing.T) {
	r := NewRegistry(seedPolicies())
	p, ok := r.Get("strict")
	if !ok {
		t.Fatal("expected to find strict")
	}
	if p.Limit != 2 {
		t.Fatalf("limit=%d want 2", p.Limit)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("expected miss for nope")
	}
}

func TestRegistry_ListIsSortedSnapshot(t *testing.T) {
	r := NewRegistry(seedPolicies())
	all := r.List()
	if len(all) != 2 {
		t.Fatalf("len=%d want 2", len(all))
	}
	if all[0].Name != "strict" || all[1].Name != "sw" {
		t.Fatalf("order = %q,%q want strict,sw (sorted)", all[0].Name, all[1].Name)
	}
	// Mutating the returned slice must not affect the registry.
	all[0].Limit = 999
	if p, _ := r.Get("strict"); p.Limit != 2 {
		t.Fatal("registry mutated via returned slice")
	}
}

func TestRegistry_CreateThenDuplicate(t *testing.T) {
	r := NewRegistry(nil)
	p := config.PolicyConfig{Name: "p", Algorithm: "tokenbucket", Limit: 10, RefillPerSecond: 1}
	if err := r.Create(p); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := r.Create(p); err == nil {
		t.Fatal("expected ErrPolicyExists on duplicate create")
	} else {
		var existsErr ErrPolicyExists
		if !errors.As(err, &existsErr) {
			t.Fatalf("want ErrPolicyExists, got %T: %v", err, err)
		}
	}
}

func TestRegistry_CreateValidatesPolicy(t *testing.T) {
	r := NewRegistry(nil)
	cases := []config.PolicyConfig{
		{Name: "", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1},  // empty name
		{Name: "p", Algorithm: "tokenbucket", Limit: 0, RefillPerSecond: 1}, // zero limit
		{Name: "p", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 0}, // zero refill
		{Name: "p", Algorithm: "bogus", Limit: 1},                           // unknown algo
	}
	for i, c := range cases {
		if err := r.Create(c); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
	if r.Size() != 0 {
		t.Fatalf("invalid creates should not be stored; size=%d", r.Size())
	}
}

func TestRegistry_UpdateExistingAndMissing(t *testing.T) {
	r := NewRegistry(seedPolicies())
	updated := config.PolicyConfig{Name: "strict", Algorithm: "tokenbucket", Limit: 99, RefillPerSecond: 5}
	if err := r.Update(updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	p, _ := r.Get("strict")
	if p.Limit != 99 {
		t.Fatalf("after update limit=%d want 99", p.Limit)
	}
	missing := config.PolicyConfig{Name: "ghost", Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1}
	if err := r.Update(missing); err == nil {
		t.Fatal("expected ErrPolicyNotFound updating missing")
	}
}

func TestRegistry_UpdateValidates(t *testing.T) {
	r := NewRegistry(seedPolicies())
	bad := config.PolicyConfig{Name: "strict", Algorithm: "tokenbucket", Limit: 0, RefillPerSecond: 1}
	if err := r.Update(bad); err == nil {
		t.Fatal("expected validation error on invalid update")
	}
	// The existing policy must be unchanged after a failed update.
	p, _ := r.Get("strict")
	if p.Limit != 2 {
		t.Fatalf("existing policy clobbered by failed update; limit=%d want 2", p.Limit)
	}
}

func TestRegistry_DeleteExistingAndMissing(t *testing.T) {
	r := NewRegistry(seedPolicies())
	if err := r.Delete("strict"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := r.Get("strict"); ok {
		t.Fatal("strict should be gone after delete")
	}
	if err := r.Delete("strict"); err == nil {
		t.Fatal("expected ErrPolicyNotFound deleting missing")
	}
}

func TestRegistry_Size(t *testing.T) {
	r := NewRegistry(seedPolicies())
	if r.Size() != 2 {
		t.Fatalf("size=%d want 2", r.Size())
	}
}

// TestRegistry_ConcurrentReadWriteMutations runs readers and writers in
// parallel to confirm the RWMutex keeps the registry consistent under
// -race. It asserts no panic, a non-negative final size, and that every Get
// returns a fully-formed policy when it hits.
func TestRegistry_ConcurrentReadWriteMutations(t *testing.T) {
	r := NewRegistry(seedPolicies())
	const g = 16
	var wg sync.WaitGroup
	wg.Add(g * 3)

	// Readers
	for i := 0; i < g; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if p, ok := r.Get("strict"); ok {
					_ = p.Limit
				}
				_ = r.List()
				_ = r.Size()
			}
		}()
	}
	// Creators (unique names)
	for i := 0; i < g; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				name := "c-" + itoa2(id) + "-" + itoa2(j)
				_ = r.Create(config.PolicyConfig{Name: name, Algorithm: "tokenbucket", Limit: 1, RefillPerSecond: 1})
			}
		}(i)
	}
	// Updaters + deleters on the seeded "sw"
	for i := 0; i < g; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if id%2 == 0 {
					_ = r.Update(config.PolicyConfig{Name: "sw", Algorithm: "slidingwindow", Limit: int64(j + 1), Window: time.Minute})
				} else {
					_ = r.Delete("sw")
					_ = r.Create(config.PolicyConfig{Name: "sw", Algorithm: "slidingwindow", Limit: 5, Window: time.Minute})
				}
			}
		}(i)
	}
	wg.Wait()
	if r.Size() < 1 {
		t.Fatalf("size=%d after concurrent ops, want >= 1", r.Size())
	}
}

// itoa2 is a tiny local int->string to avoid pulling strconv into the test
// (kept self-contained).
func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
