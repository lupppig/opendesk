package front

import (
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// cleanBody fixes the images in a Front message body before the engine sanitizes it:
//
//   - An inline image Front stores points at a path in Front's own app, with a front-cid
//     attribute (or a cid: link, or only a resource_link_id) naming the attachment. It gets
//     the attachment's link, which the engine swaps for the copied file.
//   - Front's read receipt (a tracking pixel on app.frontapp.com) is dropped.
//   - A remote image Front proxied (ext.frontusercontent.com/v1/proxy/<token>#<original>)
//     gets its original link back from the part after '#'.
//
// An image left pointing into Front (unmatched, or a proxy without its original) would
// never load once imported, so it becomes its alt text, or "[image]". Everything else is
// passed through byte for byte.
func cleanBody(raw string, atts []attachment) string {
	if !strings.Contains(strings.ToLower(raw), "<img") {
		return raw
	}
	byCID := map[string]string{}
	byLink := map[string]string{}
	for _, a := range atts {
		u := strings.TrimSpace(a.URL)
		if u == "" {
			continue
		}
		if cid := normCID(a.Metadata.CID); cid != "" {
			byCID[cid] = u
		}
		if id, ok := strings.CutPrefix(a.ID, "fil_"); ok && id != "" {
			byLink[strings.ToLower(id)] = u
		}
	}
	var b strings.Builder
	z := nethtml.NewTokenizer(strings.NewReader(raw))
	for {
		tt := z.Next()
		if tt == nethtml.ErrorToken {
			if z.Err() != io.EOF {
				// Unparseable tail: keep it as it was.
				b.Write(z.Raw())
			}
			break
		}
		rawTok := string(z.Raw())
		if tt != nethtml.StartTagToken && tt != nethtml.SelfClosingTagToken {
			b.WriteString(rawTok)
			continue
		}
		tok := z.Token()
		if tok.DataAtom != atom.Img {
			b.WriteString(rawTok)
			continue
		}
		if out, changed := rewriteImg(tok, byCID, byLink); changed {
			b.WriteString(out)
		} else {
			b.WriteString(rawTok)
		}
	}
	return b.String()
}

// textLink finds a link in a plain-text body, with the brackets Front puts around an
// image's link when it renders a body as text.
var textLink = regexp.MustCompile(`\[?https?://[^\s\]<>"]+\]?`)

// cleanText drops Front's read receipt links from a plain-text body. Front writes the
// pixel as "[https://app.frontapp.com/api/1/noauth/.../seen/...]", so every quoted reply
// carries one. A line left with only quote marks is dropped too. Other text is untouched.
func cleanText(raw string) string {
	if !strings.Contains(raw, "/noauth/") {
		return raw
	}
	lines := strings.SplitAfter(raw, "\n")
	var b strings.Builder
	for _, line := range lines {
		changed := false
		out := textLink.ReplaceAllStringFunc(line, func(m string) string {
			u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(m, "["), "]"))
			if err != nil || !receipt(u) {
				return m
			}
			changed = true
			return ""
		})
		if changed && strings.Trim(out, "> \t\r\n") == "" {
			continue
		}
		b.WriteString(out)
	}
	return b.String()
}

func normCID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "cid:"), "CID:")
	return strings.ToLower(strings.Trim(s, "<>"))
}

// rewriteImg returns what an img tag becomes, and whether it changed.
func rewriteImg(t nethtml.Token, byCID, byLink map[string]string) (string, bool) {
	var src, fcid, alt string
	for _, a := range t.Attr {
		switch strings.ToLower(a.Key) {
		case "src":
			src = strings.TrimSpace(a.Val)
		case "front-cid":
			fcid = a.Val
		case "alt":
			alt = strings.TrimSpace(a.Val)
		}
	}
	placeholder := "[image]"
	if alt != "" {
		placeholder = html.EscapeString(alt)
	}
	if u, ok := byCID[normCID(fcid)]; ok && fcid != "" {
		return render(t, u), true
	}
	if strings.HasPrefix(strings.ToLower(src), "cid:") {
		if u, ok := byCID[normCID(src)]; ok {
			return render(t, u), true
		}
		return placeholder, true
	}
	if src == "" || strings.HasPrefix(strings.ToLower(src), "data:") {
		return "", false
	}
	u, err := url.Parse(src)
	if err != nil {
		return placeholder, true
	}
	host := strings.ToLower(u.Hostname())
	if host == "ext.frontusercontent.com" && strings.HasPrefix(u.Path, "/v1/proxy/") {
		_, orig, _ := strings.Cut(src, "#")
		o, err := url.Parse(orig)
		if err != nil || (o.Scheme != "https" && o.Scheme != "http") || o.Host == "" {
			return placeholder, true
		}
		if receipt(o) {
			return "", true
		}
		return render(t, orig), true
	}
	if receipt(u) {
		return "", true
	}
	frontPath := u.Host == "" || host == "frontapp.com" || strings.HasSuffix(host, ".frontapp.com")
	if frontPath && strings.Contains(u.Path, "/api/1/companies/") {
		if n, err := strconv.ParseUint(u.Query().Get("resource_link_id"), 10, 64); err == nil {
			if link, ok := byLink[strconv.FormatUint(n, 36)]; ok {
				return render(t, link), true
			}
		}
		return placeholder, true
	}
	if u.Scheme == "" {
		// A relative link has nothing to load once it is out of Front.
		return placeholder, true
	}
	return "", false
}

// receipt says the link is Front's read receipt pixel.
func receipt(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	if host != "frontapp.com" && !strings.HasSuffix(host, ".frontapp.com") {
		return false
	}
	return strings.Contains(u.Path, "/noauth/") && strings.Contains(u.Path, "/seen")
}

// render writes the img tag with src replaced and Front's own attribute dropped.
func render(t nethtml.Token, src string) string {
	var b strings.Builder
	b.WriteString("<img")
	wrote := false
	for _, a := range t.Attr {
		k := strings.ToLower(a.Key)
		if k == "front-cid" {
			continue
		}
		v := a.Val
		if k == "src" {
			if wrote {
				continue
			}
			v, wrote = src, true
		}
		b.WriteString(" " + k + `="` + html.EscapeString(v) + `"`)
	}
	if !wrote {
		b.WriteString(` src="` + html.EscapeString(src) + `"`)
	}
	b.WriteString(">")
	return b.String()
}
