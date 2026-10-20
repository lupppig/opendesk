package templates

import (
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
)

// HistoryItem is an earlier public reply quoted under a new one.
type HistoryItem struct {
	Author string
	At     time.Time
	HTML   string // sanitized
	Text   string
}

// Email is a rendered customer email.
type Email struct {
	HTML string
	Text string
}

// Parts are what goes into a layout. Content and Signature are sanitized HTML.
type Parts struct {
	Content     string
	ContentText string
	Signature   string
	History     []HistoryItem
	// Forward quotes all of History (up to 10), whatever the design's history setting,
	// and also under a custom layout without a history slot.
	Forward bool
}

var fonts = map[string]string{
	"system": `-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif`,
	"sans":   `Helvetica,Arial,sans-serif`,
	"serif":  `Georgia,'Times New Roman',Times,serif`,
}

// historyPolicy keeps the words of a quoted message, not its images or layout.
var historyPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.SkipElementsContent(skipContent...)
	p.AllowElements("p", "br", "strong", "b", "em", "i", "u", "s", "ul", "ol", "li", "blockquote", "code", "pre", "h1", "h2", "h3", "h4", "div", "span")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}()

// RenderEmail puts parts into the brand's layout.
func (c *Context) RenderEmail(d Design, p Parts) Email {
	o := d.Layout.normalized()
	accent := o.Accent
	if accent == "" {
		accent = c.BrandColor
	}
	font := fonts[o.Font]
	if font == "" {
		font = fonts["system"]
	}
	mode := o.History
	if p.Forward {
		mode = "all"
	}
	var hist []HistoryItem
	switch mode {
	case "last":
		if len(p.History) > 0 {
			hist = p.History[:1]
		}
	case "all":
		hist = p.History
		if len(hist) > 10 {
			hist = hist[:10]
		}
	}
	historyHTML, historyText := c.renderHistory(o, hist)
	// A custom layout writes its own footer.
	custom := o.Mode == "custom" && strings.TrimSpace(d.CustomHTML) != ""
	footer := ""
	if !custom && strings.TrimSpace(o.FooterHTML) != "" {
		footer = c.Fill(o.FooterHTML)
	}

	// The slots go in after sanitizing, so the message itself is never cleaned twice.
	slots := map[string]string{"content": p.Content, "signature": p.Signature, "history": historyHTML}
	var body string
	if custom {
		body = c.fillWithSlots(d.CustomHTML, slots)
		if !Parse(d.CustomHTML).Uses("content") {
			body += p.Content
		}
		if p.Forward && !Parse(d.CustomHTML).Uses("history") {
			body += historyHTML
		}
	} else {
		body = simpleBody(o, c, accent, font, p, footer, historyHTML)
	}
	doc := `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<meta name="color-scheme" content="light only"><meta name="supported-color-schemes" content="light"><style>` + baseCSS(accent, font) + `</style></head>` +
		`<body style="margin:0;padding:0;-webkit-text-size-adjust:100%">` + body + `</body></html>`

	var text strings.Builder
	text.WriteString(strings.TrimSpace(p.ContentText))
	if sig := TextOf(p.Signature); sig != "" {
		text.WriteString("\n\n-- \n" + sig)
	}
	if ft := TextOf(footer); ft != "" {
		text.WriteString("\n\n" + ft)
	}
	if historyText != "" {
		text.WriteString("\n\n" + historyText)
	}
	return Email{HTML: doc, Text: strings.TrimSpace(text.String()) + "\n"}
}

// fillWithSlots fills a custom layout: variables, then the slots in place of placeholders.
func (c *Context) fillWithSlots(src string, slots map[string]string) string {
	vals := &Values{Text: c.Values.Text, HTML: map[string]string{}, Lists: c.Values.Lists, Known: c.Values.Known, Location: c.Values.Location}
	for k, v := range c.Values.HTML {
		vals.HTML[k] = v
	}
	keys := []string{"content", "signature", "history"}
	for i, k := range keys {
		vals.HTML[k] = "zqslot" + strconv.Itoa(i) + "qz"
	}
	out := SanitizeForSend(Render(src, vals, HTML))
	for i, k := range keys {
		out = strings.ReplaceAll(out, "zqslot"+strconv.Itoa(i)+"qz", slots[k])
	}
	return out
}

func baseCSS(accent, font string) string {
	return `body{margin:0;padding:0}` +
		`.od-body{font-family:` + font + `;font-size:14px;line-height:1.6;color:#171717}` +
		`.od-body p{margin:0 0 12px}` +
		`.od-body a{color:` + accent + `}` +
		`.od-body h1,.od-body h2,.od-body h3{margin:16px 0 8px;line-height:1.3}` +
		`.od-body ul,.od-body ol{margin:0 0 12px;padding-left:22px}` +
		`.od-body blockquote{margin:0 0 12px;padding-left:12px;border-left:3px solid #E5E5E5;color:#525252}` +
		`.od-body code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:13px;background:#F5F5F5;border-radius:4px;padding:1px 4px}` +
		`.od-body pre{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:13px;background:#F5F5F5;border-radius:6px;padding:10px 12px;white-space:pre-wrap}` +
		`.od-body img{max-width:100%;height:auto}` +
		`.od-history blockquote{border-left:2px solid #D4D4D4}` +
		`@media (max-width:620px){.od-pad{padding-left:18px!important;padding-right:18px!important}}`
}

func logoHTML(c *Context, height int) string {
	if c.LogoURL == "" {
		return ""
	}
	h := strconv.Itoa(height)
	return `<img src="` + html.EscapeString(c.LogoURL) + `" alt="` + html.EscapeString(c.BrandName) + `" height="` + h +
		`" style="display:block;height:` + h + `px;width:auto;max-width:220px;border:0">`
}

func simpleBody(o LayoutOptions, c *Context, accent, font string, p Parts, footer, history string) string {
	var b strings.Builder
	sig := ""
	if strings.TrimSpace(p.Signature) != "" {
		sig = `<div class="od-signature" style="margin-top:20px;color:#404040">` + p.Signature + `</div>`
	}
	if o.Style == "branded" {
		b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#F4F4F5"><tr><td align="center" style="padding:28px 12px">`)
		b.WriteString(`<table role="presentation" class="od-body" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:600px;background:#FFFFFF;border:1px solid #E5E5E5;border-top:3px solid ` + accent + `;border-radius:10px;font-family:` + font + `">`)
		if o.Logo {
			head := logoHTML(c, 30)
			if head == "" && c.BrandName != "" {
				head = `<span style="font-size:15px;font-weight:600;color:#171717">` + html.EscapeString(c.BrandName) + `</span>`
			}
			if head != "" {
				b.WriteString(`<tr><td class="od-pad" style="padding:22px 32px 4px">` + head + `</td></tr>`)
			}
		}
		b.WriteString(`<tr><td class="od-pad" style="padding:18px 32px 26px">` + p.Content + sig)
		if history != "" {
			b.WriteString(`<div style="margin-top:24px;border-top:1px solid #EFEFEF;padding-top:16px">` + history + `</div>`)
		}
		b.WriteString(`</td></tr></table>`)
		if footer != "" {
			b.WriteString(`<table role="presentation" class="od-body" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:600px"><tr><td class="od-footer od-pad" align="center" style="padding:16px 32px 0;font-size:12px;line-height:1.5;color:#8A8A8A">` + footer + `</td></tr></table>`)
		}
		b.WriteString(`</td></tr></table>`)
		return b.String()
	}
	b.WriteString(`<div class="od-body" style="max-width:640px;padding:16px 20px;font-family:` + font + `;font-size:14px;line-height:1.6;color:#171717">`)
	if o.Logo {
		if l := logoHTML(c, 28); l != "" {
			b.WriteString(`<div style="margin:0 0 18px">` + l + `</div>`)
		}
	}
	b.WriteString(p.Content + sig)
	if footer != "" {
		b.WriteString(`<div class="od-footer" style="margin-top:28px;padding-top:12px;border-top:1px solid #EBEBEB;font-size:12px;line-height:1.5;color:#8A8A8A">` + footer + `</div>`)
	}
	if history != "" {
		b.WriteString(`<div style="margin-top:24px">` + history + `</div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// DefaultHistoryHeader introduces each quoted message.
const DefaultHistoryHeader = "On {{ message.date }}, {{ message.author }} wrote:"

func (c *Context) renderHistory(o LayoutOptions, items []HistoryItem) (string, string) {
	if len(items) == 0 {
		return "", ""
	}
	header := o.HistoryHeader
	if strings.TrimSpace(header) == "" {
		header = DefaultHistoryHeader
	}
	t := Parse(header)
	var h, txt strings.Builder
	h.WriteString(`<div class="od-history">`)
	for _, it := range items {
		vals := &Values{Text: map[string]string{
			"message.author": it.Author,
			"message.date":   it.At.In(c.Location).Format("Mon, 2 Jan 2006 at 15:04"),
		}}
		line := strings.Join(strings.Fields(t.Render(vals, Text)), " ")
		h.WriteString(`<div style="margin:0 0 6px;font-size:12px;color:#737373">` + html.EscapeString(line) + `</div>`)
		h.WriteString(`<blockquote style="margin:0 0 16px;padding:0 0 0 12px;border-left:2px solid #D4D4D4;color:#525252">` +
			strings.TrimSpace(historyPolicy.Sanitize(it.HTML)) + `</blockquote>`)
		body := it.Text
		if strings.TrimSpace(body) == "" {
			body = TextOf(it.HTML)
		}
		txt.WriteString(line + "\n")
		for _, ln := range strings.Split(strings.TrimSpace(body), "\n") {
			txt.WriteString("> " + ln + "\n")
		}
		txt.WriteString("\n")
	}
	h.WriteString(`</div>`)
	return h.String(), strings.TrimSpace(txt.String())
}

// SurveyButtons are the five score links of a satisfaction email.
func SurveyButtons(baseURL, accent string) (string, string) {
	if !ValidColor(accent) {
		accent = "#0A0A0A"
	}
	labels := [6]string{"", "Very poor", "Poor", "Okay", "Good", "Great"}
	var h, t strings.Builder
	h.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;margin:4px 0 16px"><tr>`)
	for n := 1; n <= 5; n++ {
		u := html.EscapeString(baseURL + strconv.Itoa(n))
		h.WriteString(`<td style="padding:0 6px 0 0"><a href="` + u + `" title="` + labels[n] + `" style="display:inline-block;width:44px;height:44px;line-height:42px;border:2px solid ` +
			accent + `;border-radius:8px;color:#0A0A0A;text-decoration:none;font-weight:600;font-size:16px;text-align:center">` + strconv.Itoa(n) + `</a></td>`)
		t.WriteString(strconv.Itoa(n) + " (" + labels[n] + "): " + baseURL + strconv.Itoa(n) + "\n")
	}
	h.WriteString(`</tr></table>`)
	return h.String(), t.String()
}
