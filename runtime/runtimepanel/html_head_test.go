package runtimepanel

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInjectionLocatesDocumentHeadOutsideHTMLText(t *testing.T) {
	contents := []string{
		`<script>const template = "</head>";</script>`,
		`<!-- a template may contain </head> -->`,
		`<meta data-template="</head>" data-other='</head>'>`,
		`<style>body::before { content: "</head>" }</style>`,
		`<title>literal </head> text</title>`,
		`<noscript>literal </head> text</noscript>`,
		`<noframes>literal </head> text</noframes>`,
		`<template><div></head></div><template><title></head></title></template></template>`,
		`<template><textarea></head></textarea><iframe></head></iframe><xmp></head></xmp><noembed></head></noembed></template>`,
		`<template><svg><![CDATA[> </template></head>]]></svg></template>`,
		`<script><!--<script>ignored </script> const template = "</head>"; --></script>`,
		`<script><!--<script>ignored --> const template = "</head>";</script>`,
		`<!-- <script type="module" src="/app/test/_runtime/bootstrap.js"></script> -->`,
	}
	marker := `<script type="module" src="/app/test/_runtime/bootstrap.js"></script>`
	loader := string(classicLoader("/app/test", ""))
	for _, content := range contents {
		t.Run(content, func(t *testing.T) {
			body := `<!doctype html><html><head>` + content + `</HEAD ><body>business</body></html>`
			response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
			response.Header.Set("Content-Type", "text/html")
			if err := Inject(response, "/app/test"); err != nil {
				t.Fatal(err)
			}
			first, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(body, `</HEAD >`, marker+`</HEAD >`, 1)
			want = strings.Replace(want, `</body>`, loader+`</body>`, 1)
			if string(first) != want {
				t.Fatalf("HTML text was modified: got %s; want %s", first, want)
			}
			response.Body = io.NopCloser(strings.NewReader(string(first)))
			if err := Inject(response, "/app/test"); err != nil {
				t.Fatal(err)
			}
			second, _ := io.ReadAll(response.Body)
			if string(second) != string(first) {
				t.Fatal("repeated injection duplicated the control shell")
			}
		})
	}
}

func TestClassicLoaderSkippedWhenPageDefinesCSP(t *testing.T) {
	body := `<!doctype html><html><head></head><body>business</body></html>`
	response := &http.Response{StatusCode: 200, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body))}
	response.Header.Set("Content-Type", "text/html")
	response.Header.Set("Content-Security-Policy", "script-src 'none'")
	if err := Inject(response, "/app/test"); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "data-fnpackkit-loader") {
		t.Fatal("inline loader must not be injected into a CSP page")
	}
	if !strings.Contains(string(got), `_runtime/bootstrap.js`) {
		t.Fatal("module loader should remain available for CSP pages")
	}
}

func TestIncompleteOrImplicitHeadsRemainUnmodified(t *testing.T) {
	for _, body := range []string{
		`<head><!-- </head>`,
		`<head><meta title="</head>`,
		`<head><script>const s = "</head>";`,
		`<head><style>/* </head> */`,
		`<head><template><div></head></div>`,
		`<head><body><script>const s = "</head>";</script>`,
		`<head>business text</head>`,
		`<body><div data-template="</head>"></div>`,
		`<div></head></div>`,
	} {
		response := &http.Response{StatusCode: 200, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
		response.Header.Set("Content-Type", "text/html")
		response.Header.Set("ETag", "unchanged")
		if err := Inject(response, "/app/test"); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		if err != nil || string(got) != body || response.ContentLength != int64(len(body)) || response.Header.Get("ETag") != "unchanged" {
			t.Fatalf("ambiguous response changed: body=%q err=%v", got, err)
		}
	}
}

func TestHeadBoundaryAcceptsUTF8BOMAndQuotedDoctype(t *testing.T) {
	for _, prefix := range []string{"\xef\xbb\xbf", `<!DOCTYPE html SYSTEM "quoted>identifier">`} {
		body := prefix + "<HTML><HEAD></HeAd\t><body>business</body></HTML>"
		if got := htmlHeadEnd([]byte(body)); got != strings.Index(body, "</HeAd") {
			t.Fatalf("head boundary not found: position=%d body=%q", got, body)
		}
	}
}
