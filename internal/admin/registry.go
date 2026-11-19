// Package admin provides a concurrency-safe, runtime-mutable registry of
// rate-limit policies.
//
// In Phase 1 the server held policy configuration as a plain map copied from
// the loaded config at startup, which meant policies could only change via a
// full process restart. Phase 2 replaces that map with [Registry], so the
// admin REST API can create, update, and delete policies at runtime while
// Check traffic reads them concurrently and safely.
//
// Registry is the single source of truth for policy lookups during Check:
// the server's hot path calls Get, while the admin handlers call Create /
// Update / Delete. All operations take an RWMutex — reads are RLock'd so
// concurrent Checks do not serialize on each other.
package admin

import (
	"fmt"
	"sort"
	"sync"

	"github.com/Abolfazlrwm/rategate/internal/config"
)

// Registry is a concurrency-safe, runtime-mutable store of named rate-limit
// policies. The zero value is not usable; construct with [NewRegistry].
type Registry struct {
	mu       sync.RWMutex
	policies map[string]config.PolicyConfig
}

// NewRegistry returns a Registry seeded with the given policies (typically
// from [config.Config.PolicyMap] at startup). A nil map is treated as empty.
func NewRegistry(initial map[string]config.PolicyConfig) *Registry {
	r := &Registry{policies: make(map[string]config.PolicyConfig, len(initial))}
	for k, v := range initial {
		r.policies[k] = v
	}
	return r
}

// Get returns the policy by name and whether it existed. Safe for concurrent
// use with Create/Update/Delete.
func (r *Registry) Get(name string) (config.PolicyConfig, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.policies[name]
	return p, ok
}

// List returns a snapshot of all policies sorted by name for stable REST
// output. The returned slice is a copy and may be modified freely.
func (r *Registry) List() []config.PolicyConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.policies))
	for n := range r.policies {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]config.PolicyConfig, 0, len(names))
	for _, n := range names {
		out = append(out, r.policies[n])
	}
	return out
}

// Create adds a new policy. It validates p first and returns
// [ErrPolicyExists] if a policy with the same name is already present.
func (r *Registry) Create(p config.PolicyConfig) error {
	if err := p.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.policies[p.Name]; ok {
		return ErrPolicyExists{Name: p.Name}
	}
	r.policies[p.Name] = p
	return nil
}

// Update overwrites an existing policy. It validates p first and returns
// [ErrPolicyNotFound] if the named policy does not exist. The name in the
// request body must match the name in the resource path.
func (r *Registry) Update(p config.PolicyConfig) error {
	if err := p.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.policies[p.Name]; !ok {
		return ErrPolicyNotFound{Name: p.Name}
	}
	r.policies[p.Name] = p
	return nil
}

// Delete removes a policy. Returns [ErrPolicyNotFound] if it does not exist.
func (r *Registry) Delete(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.policies[name]; !ok {
		return ErrPolicyNotFound{Name: name}
	}
	delete(r.policies, name)
	return nil
}

// Size returns the number of registered policies. Intended for metrics and
// health output, not the hot path.
func (r *Registry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.policies)
}

// ErrPolicyExists is returned by Create when a policy with the given name is
// already registered.
type ErrPolicyExists struct{ Name string }

func (e ErrPolicyExists) Error() string {
	return fmt.Sprintf("rategate: policy %q already exists", e.Name)
}

// ErrPolicyNotFound is returned by Update / Delete / Get-style lookups when the
// named policy is absent.
type ErrPolicyNotFound struct{ Name string }

func (e ErrPolicyNotFound) Error() string {
	return fmt.Sprintf("rategate: policy %q not found", e.Name)
}
