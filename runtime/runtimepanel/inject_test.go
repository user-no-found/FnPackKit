package runtimepanel

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestHTMLNonDocumentsPreserveBodyAndHeaders(t *testing.T) {
	for _, body := range []string{"", "syntheticCsrfToken123", "<div>fragment</div>"} {
		t.Run(body, func(t *testing.T) {
			response := &http.Response{StatusCode: 200, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
			response.Header.Set("Content-Type", "text/html; charset=UTF-8")
			response.Header.Add("Set-Cookie", "fn2auth_41001_session=synthetic; Path=/app/test/; HttpOnly")
			response.Header.Set("ETag", "unchanged")
			response.Header.Set("Content-Security-Policy", "script-src 'none'")
			previous := response.Header.Clone()
			if err := Inject(response, "/app/test"); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(response.Body)
			if err != nil || string(got) != body || response.ContentLength != int64(len(body)) || !reflect.DeepEqual(previous, response.Header) {
				t.Fatalf("non-document response was changed: body=%q headers=%v err=%v", got, response.Header, err)
			}
		})
	}
}
