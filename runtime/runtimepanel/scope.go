package runtimepanel

// Scope describes the effect of backend controls; it grants no permissions.
type Scope string

const (
	ScopeUser   Scope = "user"
	ScopeShared Scope = "shared"
)

func (scope Scope) Valid() bool {
	return scope == "" || scope == ScopeUser || scope == ScopeShared
}
