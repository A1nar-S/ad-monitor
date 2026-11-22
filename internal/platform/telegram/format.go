package telegram

import (
	"errors"
	"html"
	"net/http"
	"strings"
)

// HTML turns the markdown the bot and the notifier write (Discord's:
// **bold**, [text](<url>), <url>, \-escapes) into Telegram's HTML. Anything
// else is plain text.
func HTML(md string) string {
	var b strings.Builder
	bold := false
	for i := 0; i < len(md); {
		rest := md[i:]
		switch {
		case rest[0] == '\\' && len(rest) > 1 && isPunct(rest[1]):
			b.WriteString(html.EscapeString(rest[1:2]))
			i += 2
		case strings.HasPrefix(rest, "**") && (bold || strings.Contains(rest[2:], "**")):
			if bold {
				b.WriteString("</b>")
			} else {
				b.WriteString("<b>")
			}
			bold = !bold
			i += 2
		case rest[0] == '[':
			text, url, n, ok := mdLink(rest)
			if !ok {
				b.WriteString("[")
				i++
				continue
			}
			b.WriteString(`<a href="` + html.EscapeString(url) + `">` + HTML(text) + "</a>")
			i += n
		case rest[0] == '<':
			url, n, ok := angleURL(rest)
			if !ok {
				b.WriteString("&lt;")
				i++
				continue
			}
			b.WriteString(html.EscapeString(url))
			i += n
		default:
			b.WriteString(html.EscapeString(rest[:1]))
			i++
		}
	}
	if bold {
		b.WriteString("</b>")
	}
	return b.String()
}

// mdLink reads [text](url) or [text](<url>) at the start of s.
func mdLink(s string) (text, url string, n int, ok bool) {
	end := -1
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == ']' {
			end = i
			break
		}
	}
	if end < 0 || !strings.HasPrefix(s[end+1:], "(") {
		return "", "", 0, false
	}
	text, rest := s[1:end], s[end+2:]
	closing := ")"
	if strings.HasPrefix(rest, "<") {
		rest, closing = rest[1:], ">)"
	}
	j := strings.Index(rest, closing)
	if j < 0 {
		return "", "", 0, false
	}
	url = rest[:j]
	if !isWebURL(url) {
		return "", "", 0, false
	}
	return text, url, len(s) - len(rest) + j + len(closing), true
}

// angleURL reads <https://...> at the start of s.
func angleURL(s string) (url string, n int, ok bool) {
	j := strings.IndexByte(s, '>')
	if j < 0 {
		return "", 0, false
	}
	url = s[1:j]
	if !isWebURL(url) || strings.ContainsAny(url, " \n<") {
		return "", 0, false
	}
	return url, j + 1, true
}

func isWebURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func isPunct(c byte) bool {
	return c < 0x80 && strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", c) >= 0
}

// Undeliverable reports errors no retry can fix: the user blocked the bot or
// never started it, the chat is gone.
func Undeliverable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	if e.Code == http.StatusForbidden {
		return true
	}
	d := strings.ToLower(e.Description)
	return e.Code == http.StatusBadRequest &&
		(strings.Contains(d, "chat not found") || strings.Contains(d, "user not found") || strings.Contains(d, "peer_id_invalid"))
}
