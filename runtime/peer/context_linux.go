package peer

import (
	"context"
	"net"
)

type authenticatedPeerKey struct{}

// ConnContext marks only connections accepted and verified by this listener.
// Assign it to http.Server.ConnContext; it cannot authenticate arbitrary sockets.
func (listener *Listener) ConnContext(ctx context.Context, conn net.Conn) context.Context {
	accepted, ok := conn.(*lifetimeConn)
	if !ok || accepted.listener != listener {
		return ctx
	}
	return context.WithValue(ctx, authenticatedPeerKey{}, accepted.uid)
}

// TrustedUID returns the kernel UID attached by a verified listener's ConnContext.
func TrustedUID(ctx context.Context) (uint32, bool) {
	uid, ok := ctx.Value(authenticatedPeerKey{}).(uint32)
	return uid, ok
}
