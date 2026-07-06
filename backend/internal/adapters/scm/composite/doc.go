// Package composite implements a deterministic, per-repo dispatching
// scmobserve.Provider that wraps an ordered list of named provider adapters
// (e.g. github, gitlab). It lets a single Observer poll several SCM hosts by
// routing each call to the provider whose name matches the repo/ref already
// stamped by that provider's own ParseRepository.
//
// # Routing
//
// ParseRepository tries each entry in order and returns the first ok result;
// the winning provider's name is what it stamps onto the returned
// ports.SCMRepo.Provider. Every other method routes by looking up
// repo.Provider (or ref.Repo.Provider) in a name->provider map built from the
// same entries, so there is no re-parsing or host sniffing on the hot path.
//
// A repo/ref whose Provider name has no matching entry is a caller/config
// bug (e.g. a stored PR row from a provider no longer configured); those
// calls return a clear "composite scm: no provider %q" error (or the zero
// value alongside it) rather than silently picking an arbitrary provider.
//
// # Out of scope
//
// This package contains no HTTP or provider-specific logic; it is a thin
// routing layer over the scmobserve.Provider interface.
package composite
