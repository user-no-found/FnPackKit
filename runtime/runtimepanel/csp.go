package runtimepanel

import (
	"errors"
	"net/http"
	"strings"
)

// scriptNonce preserves existing strict script policies without adding script sources or weakening CSP.
func scriptNonce(header http.Header) (string, error) {
	var candidates map[string]bool
	for _, value := range header.Values("Content-Security-Policy") {
		directives := make(map[string][]string)
		for _, directive := range strings.Split(value, ";") {
			fields := strings.Fields(directive)
			if len(fields) > 0 {
				if _, ok := directives[fields[0]]; !ok {
					directives[fields[0]] = fields[1:]
				}
			}
		}
		sources, ok := directives["script-src-elem"]
		if !ok {
			sources, ok = directives["script-src"]
		}
		if !ok {
			sources = directives["default-src"]
		}
		allowed := make(map[string]bool)
		for _, source := range sources {
			if !strings.HasPrefix(source, "'nonce-") || !strings.HasSuffix(source, "'") {
				continue
			}
			nonce := strings.TrimSuffix(strings.TrimPrefix(source, "'nonce-"), "'")
			if len(nonce) < 1 || len(nonce) > 256 {
				return "", errors.New("invalid backend CSP nonce")
			}
			for _, character := range nonce {
				if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("+/=_-", character)) {
					return "", errors.New("invalid backend CSP nonce")
				}
			}
			allowed[nonce] = true
		}
		if len(allowed) == 0 {
			continue
		}
		if candidates == nil {
			candidates = allowed
		} else {
			for nonce := range candidates {
				if !allowed[nonce] {
					delete(candidates, nonce)
				}
			}
		}
	}
	if candidates != nil && len(candidates) == 0 {
		return "", errors.New("backend CSP nonce policies disagree")
	}
	for nonce := range candidates {
		return nonce, nil
	}
	return "", nil
}
