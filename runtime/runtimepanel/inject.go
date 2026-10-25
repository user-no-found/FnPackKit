package runtimepanel

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Inject attaches the control shell to HTML documents; token and fragment responses stay unchanged.
func Inject(response *http.Response, prefix string) error {
	if response.Request != nil && response.Request.Method == http.MethodHead {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/html") || response.StatusCode != 200 {
		return nil
	}
	if !ValidPrefix(prefix) {
		return errors.New("invalid runtime panel prefix")
	}
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return errors.New("runtime panel requires uncompressed HTML")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	response.Body.Close()
	if err != nil {
		return err
	}
	if len(body) > 8*1024*1024 {
		return errors.New("runtime HTML exceeds limit")
	}
	index := htmlHeadEnd(body)
	if index < 0 {
		response.Body = io.NopCloser(bytes.NewReader(body))
		return nil
	}

	nonce, err := scriptNonce(response.Header)
	if err != nil {
		return err
	}
	attribute := ""
	if nonce != "" {
		attribute = ` nonce="` + nonce + `"`
	}
	marker := []byte(`<script type="module"` + attribute + ` src="` + prefix + `/_runtime/bootstrap.js"></script>`)
	if !bytes.HasSuffix(body[:index], marker) {
		body = bytes.Join([][]byte{body[:index], marker, body[index:]}, nil)
	}
	// Some fnOS host shells execute inline/classic body scripts but not injected
	// module scripts. Keep the module for standard browsers and add a classic
	// body loader unless the page has its own CSP.
	if response.Header.Get("Content-Security-Policy") == "" {
		body = injectClassicLoader(body, prefix, nonce)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	response.Header.Del("ETag")
	response.Header.Del("Content-MD5")
	return nil
}

func htmlBodyEnd(body []byte) int {
	return bytes.LastIndex(bytes.ToLower(body), []byte("</body>"))
}

func injectClassicLoader(body []byte, prefix, nonce string) []byte {
	if bytes.Contains(body, []byte("data-fnpackkit-loader")) {
		return body
	}
	index := htmlBodyEnd(body)
	if index < 0 {
		return body
	}
	loader := classicLoader(prefix, nonce)
	return bytes.Join([][]byte{body[:index], loader, body[index:]}, nil)
}

func classicLoader(prefix, nonce string) []byte {
	attribute := ""
	if nonce != "" {
		attribute = ` nonce="` + nonce + `"`
	}
	js := `(function(){var base=` + strconv.Quote(prefix) +
		`;var run=function(){import(base+"/_runtime/panel.js").then(function(m){return fetch(base+"/_runtime/manifest",{credentials:"same-origin",cache:"no-store"}).then(function(r){if(!r.ok)throw new Error("manifest HTTP "+r.status);return r.json()}).then(function(c){m.installPanel(c)})}).catch(function(e){console.error("FnPackKit runtime controls unavailable:",e)})};if(document.readyState==="loading"){document.addEventListener("DOMContentLoaded",run,{once:true})}else{run()}})();`
	return []byte(`<script data-fnpackkit-loader="1"` + attribute + `>` + js + `</script>`)
}
