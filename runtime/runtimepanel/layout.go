package runtimepanel

// Layout declares application-owned DOM integration, not host or user authority.
// Launcher opens controls by dispatching fnpackkit:open-controls on window.
// Avoid names a bottom toolbar the panel must leave usable in the visible viewport.
type Layout struct {
	Launcher string `json:"launcher,omitempty"`
	Avoid    string `json:"avoid,omitempty"`
}
