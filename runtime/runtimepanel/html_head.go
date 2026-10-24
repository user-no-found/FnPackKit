package runtimepanel

import (
	"bytes"
	"strings"
)

// htmlHeadEnd locates the explicit document head boundary without interpreting
// tag-shaped strings in attributes, comments, templates or HTML text elements.
// Ambiguous, incomplete or implicitly closed heads remain unmodified.
func htmlHeadEnd(body []byte) int {
	position, templates := 0, 0
	if bytes.HasPrefix(body, []byte("\xef\xbb\xbf")) {
		position = 3
	}
	inHead := false
	for position < len(body) {
		offset := bytes.IndexByte(body[position:], '<')
		if offset < 0 {
			return -1
		}
		start := position + offset
		if templates == 0 && !htmlSpaceOnly(body[position:start]) {
			return -1
		}
		if bytes.HasPrefix(body[start:], []byte("<!--")) {
			end := bytes.Index(body[start+4:], []byte("-->"))
			if end < 0 {
				return -1
			}
			position = start + 4 + end + 3
			continue
		}
		if templates > 0 && bytes.HasPrefix(body[start:], []byte("<![CDATA[")) {
			end := bytes.Index(body[start+9:], []byte("]]>"))
			if end < 0 {
				return -1
			}
			position = start + 9 + end + 3
			continue
		}
		if bytes.HasPrefix(body[start:], []byte("<!")) && htmlTextTag(body, start+2, "doctype") {
			position = htmlTagEnd(body, start+2)
			if position < 0 {
				return -1
			}
			continue
		}
		name, closing, next := htmlTagAt(body, start)
		if next < 0 {
			return -1
		}
		position = next
		if !inHead {
			if !closing && name == "head" {
				inHead = true
			} else if closing || name != "html" {
				return -1
			}
			continue
		}
		if closing && name == "head" && templates == 0 {
			return start
		}
		if name == "template" {
			if !closing {
				templates++
			} else if templates > 0 {
				templates--
			}
			continue
		}
		if closing {
			if templates == 0 && (name == "body" || name == "html" || name == "br") {
				return -1
			}
			continue
		}
		if templates == 0 {
			switch name {
			case "html", "head", "base", "basefont", "bgsound", "link", "meta":
				continue
			case "script", "style", "title", "noscript", "noframes":
			default:
				return -1
			}
		}
		switch name {
		case "script", "style", "title", "textarea", "xmp", "iframe", "noembed", "noframes", "noscript":
			position = htmlTextEnd(body, position, name)
			if position < 0 {
				return -1
			}
		case "plaintext":
			return -1
		}
	}
	return -1
}

func htmlSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f'
}

func htmlSpaceOnly(value []byte) bool {
	for _, character := range value {
		if !htmlSpace(character) {
			return false
		}
	}
	return true
}

func htmlTagAt(body []byte, start int) (string, bool, int) {
	position := start + 1
	closing := position < len(body) && body[position] == '/'
	if closing {
		position++
	}
	first := position
	if position == len(body) || !(body[position] >= 'a' && body[position] <= 'z' || body[position] >= 'A' && body[position] <= 'Z') {
		return "", false, -1
	}
	for position < len(body) && !htmlSpace(body[position]) && body[position] != '/' && body[position] != '>' {
		position++
	}
	return strings.ToLower(string(body[first:position])), closing, htmlTagEnd(body, position)
}

func htmlTagEnd(body []byte, position int) int {
	var quote byte
	for ; position < len(body); position++ {
		character := body[position]
		if quote != 0 {
			if character == quote {
				quote = 0
			}
		} else if character == '\'' || character == '"' {
			quote = character
		} else if character == '>' {
			return position + 1
		}
	}
	return -1
}

// Script text also has HTML escaped and double-escaped states. JavaScript quote
// syntax cannot determine its end: an actual </script> closes quoted JS too.
func htmlTextEnd(body []byte, position int, name string) int {
	escaped := 0
	for position < len(body) {
		if name == "script" {
			if escaped == 0 && bytes.HasPrefix(body[position:], []byte("<!--")) {
				escaped = 1
				position += 4
				continue
			}
			if escaped != 0 && bytes.HasPrefix(body[position:], []byte("-->")) {
				escaped = 0
				position += 3
				continue
			}
		}
		if body[position] != '<' {
			position++
			continue
		}
		if name == "script" && escaped == 1 && htmlTextTag(body, position+1, name) {
			escaped = 2
			position += 1 + len(name)
			continue
		}
		if position+1 < len(body) && body[position+1] == '/' && htmlTextTag(body, position+2, name) {
			if escaped == 2 {
				escaped = 1
				position += 2 + len(name)
				continue
			}
			return htmlTagEnd(body, position+2+len(name))
		}
		position++
	}
	return -1
}

func htmlTextTag(body []byte, start int, name string) bool {
	end := start + len(name)
	return end < len(body) && strings.EqualFold(string(body[start:end]), name) &&
		(htmlSpace(body[end]) || body[end] == '/' || body[end] == '>')
}
