package gatewaypolicy

import (
	"context"
	"errors"
	"net/http"
	"slices"
)

type handshakeKey struct{}
type handshakeState struct{ protocols []string }

func markHandshake(request *http.Request, protocols []string) {
	*request = *request.WithContext(context.WithValue(request.Context(), handshakeKey{}, handshakeState{protocols: protocols}))
}

// CompleteHandshake preserves application subprotocol negotiation without reflecting a ticket.
// Call from ReverseProxy.ModifyResponse. A marker is selected only when no app protocol was offered.
func CompleteHandshake(response *http.Response) error {
	if response.StatusCode != http.StatusSwitchingProtocols || response.Request == nil {
		return nil
	}
	state, ok := response.Request.Context().Value(handshakeKey{}).(handshakeState)
	if !ok {
		return nil
	}
	selected := response.Header.Values("Sec-WebSocket-Protocol")
	if len(selected) > 1 {
		return errors.New("invalid backend WebSocket protocol selection")
	}
	if len(selected) == 0 || selected[0] == "" {
		if len(state.protocols) > 0 {
			return errors.New("backend did not select an offered application protocol")
		}
		response.Header.Set("Sec-WebSocket-Protocol", HandshakeProtocol)
		return nil
	}
	if !slices.Contains(state.protocols, selected[0]) {
		return errors.New("backend selected an unoffered application protocol")
	}
	return nil
}
