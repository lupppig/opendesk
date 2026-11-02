package panels

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Blocks are an http panel's answer from version 2 on (contracts/integrations.md "The http
// answer"): a few small block types that every client renders the same way. The server reads
// them here, once: what is unknown is skipped, text is plain and clipped, links are https,
// and what reaches a client is the cleaned shape below, every value an object.
const (
	maxBlocks      = 10
	maxBlockFields = 30 // a fields block's items, a card's fields
	maxCardLines   = 20
	maxCardFooter  = 6
	maxCardBadges  = 4
	maxListItems   = 20
	maxLineTitle   = 160
	maxSubtitle    = 160
	maxBadgeText   = 40
	maxNote        = 280
	maxLinks       = 5 // a card's links, a list row's
	maxLinkLabel   = 40
	maxCardID      = 80
	// maxCacheSeconds bounds how long an answer may ask to be kept.
	maxCacheSeconds = 600
)

// The cleaned types carry a Panel prefix because huma names each API schema after its Go type,
// and a name is one schema across the whole API: a bare Block, Item, Badge or Value would clash
// with another package's (the assistant has an Item) and stop the API at startup.

// PanelBlock is one block of an http panel's answer, cleaned: a card (an order, an invoice, a
// subscription, a deal), a list of labelled values, a list of rows or a short note.
type PanelBlock struct {
	Type      string       `json:"type" enum:"card,fields,list,note"`
	ID        string       `json:"id,omitempty" doc:"card: the record's identity across answers (an order's number), which an agent's fold or unfold follows; the title without it"`
	Title     string       `json:"title,omitempty" doc:"card: required; fields and list: optional"`
	URL       string       `json:"url,omitempty" doc:"card: https only"`
	Subtitle  string       `json:"subtitle,omitempty" doc:"card"`
	Badges    []PanelBadge `json:"badges,omitempty" doc:"card: at most 4"`
	Links     []PanelLink  `json:"links,omitempty" doc:"card: the same record in other tools, shown as small buttons beside the title, at most 5"`
	Collapsed bool         `json:"collapsed,omitempty" doc:"card: starts folded to its title, links, subtitle and badges; the reader folds and opens any card"`
	Fields    []PanelItem  `json:"fields,omitempty" doc:"card: labelled values, at most 30"`
	Lines     []PanelItem  `json:"lines,omitempty" doc:"card: rows such as an order's lines, at most 20"`
	Footer    []PanelItem  `json:"footer,omitempty" doc:"card: labelled values under the lines, such as a total, at most 6"`
	Items     []PanelItem  `json:"items,omitempty" doc:"fields: labelled values, at most 30; list: rows, at most 20"`
	Text      string       `json:"text,omitempty" doc:"note"`
	Tone      string       `json:"tone,omitempty" enum:"neutral,info,success,warning,danger" doc:"note"`
}

// PanelItem is a field (label, value, url), a card's line (quantity, title, subtitle, value, url) or
// a list's row (title, subtitle, value, badge, url, links).
type PanelItem struct {
	Label    string      `json:"label,omitempty"`
	Quantity *float64    `json:"quantity,omitempty"`
	Title    string      `json:"title,omitempty"`
	Subtitle string      `json:"subtitle,omitempty"`
	Value    *PanelValue `json:"value,omitempty"`
	Badge    *PanelBadge `json:"badge,omitempty"`
	URL      string      `json:"url,omitempty" doc:"https only"`
	Links    []PanelLink `json:"links,omitempty" doc:"list rows: at most 5"`
}

// PanelLink opens the record in another tool (the shop, the ERP, the carrier): a small button
// with an icon the app draws itself, never a picture from elsewhere.
type PanelLink struct {
	Label string `json:"label" doc:"the button's name and tooltip, at most 40 characters"`
	URL   string `json:"url" doc:"https only"`
	Icon  string `json:"icon,omitempty" doc:"one of the link icons (contracts/integrations.md); absent: the label's first letter"`
}

// PanelBadge is a short word in a tone.
type PanelBadge struct {
	Text string `json:"text"`
	Tone string `json:"tone" enum:"neutral,info,success,warning,danger"`
}

// PanelValue is a cleaned value: text (a string, number or boolean as written), a date the app
// formats in the reader's locale and timezone, an amount of money or a badge.
type PanelValue struct {
	Type     string `json:"type" enum:"text,date,money,badge"`
	Text     string `json:"text,omitempty" doc:"text and badge"`
	Value    string `json:"value,omitempty" doc:"date: YYYY-MM-DD for a day, else an RFC 3339 time in UTC"`
	Relative bool   `json:"relative,omitempty" doc:"date: also say how far it is, such as in 3 days"`
	Amount   string `json:"amount,omitempty" doc:"money: a decimal, such as 129.00"`
	Currency string `json:"currency,omitempty" doc:"money: ISO 4217, such as EUR"`
	Tone     string `json:"tone,omitempty" enum:"neutral,info,success,warning,danger" doc:"badge"`
}

var (
	tones      = map[string]bool{"neutral": true, "info": true, "success": true, "warning": true, "danger": true}
	decimalRe  = regexp.MustCompile(`^-?\d{1,15}(\.\d{1,6})?$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
)

// linkIcons are the icons a link may name, all drawn by the app: generic marks, then the
// products whose logo it ships (web/src/lib/appLogos.ts). Another name is left out.
var linkIcons = map[string]bool{
	"external-link": true, "store": true, "shopping-cart": true, "receipt": true, "file-text": true, "truck": true,
	"package": true, "user": true, "building": true, "phone": true, "mail": true, "credit-card": true, "map-pin": true,
	"shopify": true, "stripe": true, "paypal": true, "square": true, "linear": true, "github": true, "atlassian": true,
	"sentry": true, "notion": true, "asana": true, "monday": true, "slack": true, "discord": true, "telegram": true,
	"intercom": true, "close": true, "box": true, "zapier": true, "zendesk": true, "helpscout": true, "freshdesk": true,
	"odoo": true,
}

// cleanID reads a card's id: a string, or a number as it is written (an ERP's record id). One
// longer than maxCardID is left out rather than cut, so two ids never become one; without an id
// the card is known by its title.
func cleanID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) != nil {
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return ""
		}
		id = n.String()
	}
	if id = cleanText(id, maxCardID+1); utf8.RuneCountInString(id) > maxCardID {
		return ""
	}
	return id
}

func toneOf(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if tones[s] {
		return s
	}
	return "neutral"
}

// blockIn is a block as a URL answers it; anything that can take several forms stays raw and is
// read on its own, so one wrong part drops that part only.
type blockIn struct {
	Type      string            `json:"type"`
	ID        json.RawMessage   `json:"id"`
	Title     string            `json:"title"`
	URL       string            `json:"url"`
	Subtitle  string            `json:"subtitle"`
	Badges    []json.RawMessage `json:"badges"`
	Links     []json.RawMessage `json:"links"`
	Collapsed json.RawMessage   `json:"collapsed"`
	Fields    []json.RawMessage `json:"fields"`
	Lines     []json.RawMessage `json:"lines"`
	Footer    []json.RawMessage `json:"footer"`
	Items     []json.RawMessage `json:"items"`
	Text      string            `json:"text"`
	Tone      string            `json:"tone"`
}

type itemIn struct {
	Label    string            `json:"label"`
	Quantity json.RawMessage   `json:"quantity"`
	Title    string            `json:"title"`
	Subtitle string            `json:"subtitle"`
	Value    json.RawMessage   `json:"value"`
	Badge    json.RawMessage   `json:"badge"`
	URL      string            `json:"url"`
	Links    []json.RawMessage `json:"links"`
}

// cleanBlocks reads an answer's blocks: at most maxBlocks that have something to show, in order.
func (s *Service) cleanBlocks(raw []json.RawMessage) []PanelBlock {
	out := []PanelBlock{}
	for _, r := range raw {
		if len(out) == maxBlocks {
			break
		}
		var in blockIn
		if json.Unmarshal(r, &in) != nil {
			continue
		}
		if b, ok := s.cleanBlock(in); ok {
			out = append(out, b)
		}
	}
	return out
}

func (s *Service) cleanBlock(in blockIn) (PanelBlock, bool) {
	b := PanelBlock{Type: strings.ToLower(strings.TrimSpace(in.Type)), Title: cleanText(in.Title, maxTitle)}
	switch b.Type {
	case "card":
		if b.Title == "" {
			return PanelBlock{}, false
		}
		b.ID = cleanID(in.ID)
		b.URL, b.Subtitle = s.cleanLink(in.URL), cleanText(in.Subtitle, maxSubtitle)
		for _, r := range in.Badges {
			if len(b.Badges) == maxCardBadges {
				break
			}
			if x := cleanBadge(r); x != nil {
				b.Badges = append(b.Badges, *x)
			}
		}
		b.Links = s.cleanLinks(in.Links)
		b.Collapsed = bytes.Equal(bytes.TrimSpace(in.Collapsed), []byte("true")) // only true folds: anything else shows the card open
		b.Fields = s.cleanFields(in.Fields, maxBlockFields)
		b.Lines = s.cleanRows(in.Lines, maxCardLines, true)
		b.Footer = s.cleanFields(in.Footer, maxCardFooter)
		return b, true
	case "fields":
		b.Items = s.cleanFields(in.Items, maxBlockFields)
		return b, len(b.Items) > 0
	case "list":
		b.Items = s.cleanRows(in.Items, maxListItems, false)
		return b, len(b.Items) > 0
	case "note":
		b.Title = ""
		b.Text, b.Tone = cleanText(in.Text, maxNote), toneOf(in.Tone)
		return b, b.Text != ""
	}
	return PanelBlock{}, false // a type this version does not know
}

// cleanFields reads labelled values: a field without a value is left out.
func (s *Service) cleanFields(raw []json.RawMessage, max int) []PanelItem {
	var out []PanelItem
	for _, r := range raw {
		if len(out) == max {
			break
		}
		var in itemIn
		if json.Unmarshal(r, &in) != nil {
			continue
		}
		v := cleanValue(in.Value)
		if v == nil {
			continue
		}
		out = append(out, PanelItem{Label: cleanText(in.Label, maxLabel), Value: v, URL: s.cleanLink(in.URL)})
	}
	return out
}

// cleanRows reads a card's lines (with a quantity) or a list's rows (with a badge): a row
// without a title is left out.
func (s *Service) cleanRows(raw []json.RawMessage, max int, lines bool) []PanelItem {
	var out []PanelItem
	for _, r := range raw {
		if len(out) == max {
			break
		}
		var in itemIn
		if json.Unmarshal(r, &in) != nil {
			continue
		}
		it := PanelItem{Title: cleanText(in.Title, maxLineTitle), Subtitle: cleanText(in.Subtitle, maxSubtitle), Value: cleanValue(in.Value), URL: s.cleanLink(in.URL)}
		if it.Title == "" {
			continue
		}
		if lines {
			it.Quantity = cleanQuantity(in.Quantity)
		} else {
			it.Badge, it.Links = cleanBadge(in.Badge), s.cleanLinks(in.Links)
		}
		out = append(out, it)
	}
	return out
}

// cleanLinks reads a card's or a row's links: at most maxLinks with a label and an https url; an
// icon that is not one of linkIcons is left out, and the app shows the label's first letter.
func (s *Service) cleanLinks(raw []json.RawMessage) []PanelLink {
	var out []PanelLink
	for _, r := range raw {
		if len(out) == maxLinks {
			break
		}
		var in struct {
			Label string `json:"label"`
			URL   string `json:"url"`
			Icon  string `json:"icon"`
		}
		if json.Unmarshal(r, &in) != nil {
			continue
		}
		l := PanelLink{Label: cleanText(in.Label, maxLinkLabel), URL: s.cleanLink(in.URL), Icon: strings.ToLower(strings.TrimSpace(in.Icon))}
		if l.Label == "" || l.URL == "" {
			continue
		}
		if !linkIcons[l.Icon] {
			l.Icon = ""
		}
		out = append(out, l)
	}
	return out
}

func cleanQuantity(raw json.RawMessage) *float64 {
	var q float64
	if len(raw) == 0 || json.Unmarshal(raw, &q) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil || !decimalRe.MatchString(strings.TrimSpace(s)) {
			return nil
		}
		_ = json.Unmarshal([]byte(strings.TrimSpace(s)), &q)
	}
	if math.IsNaN(q) || math.Abs(q) > 1e9 {
		return nil
	}
	return &q
}

func cleanBadge(raw json.RawMessage) *PanelBadge {
	var in struct {
		Text string `json:"text"`
		Tone string `json:"tone"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return nil
	}
	b := PanelBadge{Text: cleanText(in.Text, maxBadgeText), Tone: toneOf(in.Tone)}
	if b.Text == "" {
		return nil
	}
	return &b
}

// cleanValue reads a value: a string, a number or a boolean is text as written; an object is
// typed (text, date, money, badge). Anything else, or a typed value that does not read, is nil.
func cleanValue(raw json.RawMessage) *PanelValue {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	text := func(s string) *PanelValue {
		if s = cleanText(s, maxValue); s == "" {
			return nil
		}
		return &PanelValue{Type: "text", Text: s}
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return text(s)
	case 't', 'f':
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return nil
		}
		return text(valueText(b))
	case '{':
	default:
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return nil // null, or a list
		}
		return text(n.String())
	}
	var in struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Value    string          `json:"value"`
		Relative bool            `json:"relative"`
		Amount   json.RawMessage `json:"amount"`
		Currency string          `json:"currency"`
		Tone     string          `json:"tone"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(in.Type)) {
	case "text":
		return text(in.Text)
	case "date":
		d, ok := cleanDate(in.Value)
		if !ok {
			return nil
		}
		return &PanelValue{Type: "date", Value: d, Relative: in.Relative}
	case "money":
		amount, ok := cleanAmount(in.Amount)
		currency := strings.ToUpper(strings.TrimSpace(in.Currency))
		if !ok || !currencyRe.MatchString(currency) {
			return nil
		}
		return &PanelValue{Type: "money", Amount: amount, Currency: currency}
	case "badge":
		b := cleanBadge(raw)
		if b == nil {
			return nil
		}
		return &PanelValue{Type: "badge", Text: b.Text, Tone: b.Tone}
	}
	return nil
}

// cleanDate reads a day (YYYY-MM-DD, kept as it is: a day has no timezone) or a time, with an
// offset or in UTC without one (2026-11-04T10:00:00 or 2026-11-04 10:00:00), answered in UTC.
func cleanDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.Format(time.DateOnly), true
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", time.DateTime, "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			if t.Year() < 1 || t.Year() > 9999 {
				return "", false
			}
			return t.UTC().Format(time.RFC3339), true
		}
	}
	return "", false
}

// cleanAmount reads a decimal amount, given as a string or a number, as a decimal string.
func cleanAmount(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	var s string
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &s) != nil {
			return "", false
		}
		s = strings.TrimSpace(s)
	} else {
		var n json.Number
		if json.Unmarshal(raw, &n) != nil {
			return "", false
		}
		s = n.String()
	}
	if !decimalRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// cacheFor is how long an answer asks to be kept: cache_seconds from 0 (not kept) to 600,
// cacheTTL when it does not say or says something else.
func cacheFor(raw json.RawMessage) time.Duration {
	var n *float64
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil || n == nil || *n < 0 {
		return cacheTTL
	}
	return time.Duration(math.Min(math.Floor(*n), maxCacheSeconds)) * time.Second
}
