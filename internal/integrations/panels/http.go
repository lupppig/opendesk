package panels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/version"
)

const (
	// maxHTTPAnswer bounds what a panel URL answers.
	maxHTTPAnswer = 64 << 10
	// What a panel shows of an answer: at most 10 sections of 30 values each.
	maxSections     = 10
	maxSectionItems = 30
	maxTitle        = 80
	maxLabel        = 80
	maxValue        = 500
)

// lookupRequest is what OpenDesk posts to an http panel's URL. Later fields only add to it.
type lookupRequest struct {
	PanelID     uuid.UUID     `json:"panel_id"`
	WorkspaceID uuid.UUID     `json:"workspace_id"`
	Ticket      lookupTicket  `json:"ticket"`
	Contact     lookupContact `json:"contact"`
	// Refresh is true when an agent asked again (the panel's refresh button).
	Refresh bool `json:"refresh"`
	// Locale is the viewer's interface language, a lowercase base language tag ("fr", "en",
	// "de"), so the URL can answer in it; left out when the reader's app did not say.
	Locale string `json:"locale,omitempty"`
}

// localeRe is a viewer's language as a panel's request carries it: a base language tag.
var localeRe = regexp.MustCompile(`^[a-z]{2,3}$`)

// viewerLocale is the language the reader's app said it shows (GET …/panels/{panelId}?locale=fr),
// or "" for anything but a lowercase base language tag.
func viewerLocale(s string) string {
	if localeRe.MatchString(s) {
		return s
	}
	return ""
}

type lookupTicket struct {
	ID      uuid.UUID `json:"id"`
	Number  int64     `json:"number"`
	Subject string    `json:"subject"`
	// References is what the panel's pattern found in the subject and the newest messages.
	References []string `json:"references"`
}

type lookupContact struct {
	ID     *uuid.UUID `json:"id"`
	Email  string     `json:"email"`
	Name   string     `json:"name"`
	Emails []string   `json:"emails"`
	Phone  string     `json:"phone,omitempty"`
}

// answer is what an http panel's URL answers: version 1 sections, or version 2 blocks.
type answer struct {
	Version      json.RawMessage   `json:"version"`
	Blocks       []json.RawMessage `json:"blocks"`
	CacheSeconds json.RawMessage   `json:"cache_seconds"`
	Sections     []struct {
		Title string `json:"title"`
		Items []struct {
			Label string `json:"label"`
			Value any    `json:"value"`
			URL   string `json:"url"`
		} `json:"items"`
	} `json:"sections"`
}

// blocksAnswer is true for a version 2 answer: one with blocks, or that says version 2 or later.
func (a *answer) blocksAnswer() bool {
	var v float64
	return a.Blocks != nil || (json.Unmarshal(a.Version, &v) == nil && v >= 2)
}

// sign is the OD-Signature value of body at t, the scheme outgoing webhooks use
// (contracts/events.md "Webhooks"): t=<unix>,v1=HMAC-SHA256(secret, "<unix>.<body>").
func sign(secret []byte, t int64, body []byte) string {
	ts := strconv.FormatInt(t, 10)
	return "t=" + ts + ",v1=" + crypto.HMACHex(secret, ts+"."+string(body))
}

// cleanText is one line of plain text: control characters dropped, spaces collapsed, clipped.
func cleanText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return clip(strings.Join(strings.Fields(s), " "), n)
}

// cleanLink keeps an absolute https link (http on a development instance); anything else
// is dropped and the value shows as plain text. Links are only shown, never fetched.
func (s *Service) cleanLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2000 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && s.guard.Dev)) {
		return ""
	}
	return u.String()
}

// readHTTP posts the ticket and contact to the panel's URL and fills d with its blocks or
// sections. It answers how long to keep the answer: the answer's cache_seconds, else cacheTTL.
func (s *Service) readHTTP(ctx context.Context, x *row, sub subject, d *Data) (time.Duration, error) {
	ttl := cacheTTL
	secret, err := s.d.Box.Open(x.SecretEnc)
	if err != nil {
		return ttl, err
	}
	if sub.References == nil {
		sub.References = []string{}
	}
	if sub.Emails == nil {
		sub.Emails = []string{}
	}
	body, err := json.Marshal(lookupRequest{PanelID: x.ID, WorkspaceID: x.WorkspaceID, Refresh: sub.Refresh, Locale: sub.Locale,
		Ticket:  lookupTicket{ID: sub.TicketID, Number: sub.TicketNumber, Subject: sub.Subject, References: sub.References},
		Contact: lookupContact{ID: sub.ContactID, Email: sub.Email, Name: sub.Name, Emails: sub.Emails, Phone: sub.Phone}})
	if err != nil {
		return ttl, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, x.Target, bytes.NewReader(body))
	if err != nil {
		return ttl, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "opendesk-panels/"+version.Version)
	req.Header.Set("OD-Signature", sign(secret, time.Now().Unix(), body))
	req.Header.Set("OD-Panel-Id", x.ID.String())
	res, err := s.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // never the URL: a path or query can carry a secret
		}
		return ttl, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxHTTPAnswer+1))
	if err != nil {
		return ttl, err
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return ttl, &authError{fmt.Sprintf("The panel's URL refused the request (%d): check its signing secret.", res.StatusCode)}
	case res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusNotFound:
		d.State = "empty"
		return ttl, nil
	case res.StatusCode >= 300:
		return ttl, &answerError{fmt.Sprintf("The panel's URL answered %d.", res.StatusCode), "status"}
	}
	if len(raw) > maxHTTPAnswer {
		return ttl, &answerError{"The panel's answer is larger than 64 KB.", "answer"}
	}
	var answer answer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return ttl, &answerError{"The panel's answer is not the expected JSON.", "answer"}
	}
	ttl = cacheFor(answer.CacheSeconds)
	d.State = "ok"
	if answer.blocksAnswer() {
		if d.Blocks = s.cleanBlocks(answer.Blocks); len(d.Blocks) == 0 {
			d.State, d.Blocks = "empty", nil
		}
		return ttl, nil
	}
	d.Sections = []Section{}
	for _, sec := range answer.Sections {
		if len(d.Sections) == maxSections {
			break
		}
		out := Section{Title: cleanText(sec.Title, maxTitle), Items: []SectionItem{}}
		for _, it := range sec.Items {
			if len(out.Items) == maxSectionItems {
				break
			}
			item := SectionItem{Label: cleanText(it.Label, maxLabel), Value: cleanText(valueText(it.Value), maxValue), URL: s.cleanLink(it.URL)}
			if item.Label == "" && item.Value == "" {
				continue
			}
			out.Items = append(out.Items, item)
		}
		if len(out.Items) > 0 {
			d.Sections = append(d.Sections, out)
		}
	}
	if len(d.Sections) == 0 {
		d.State, d.Sections = "empty", nil
	}
	return ttl, nil
}

// valueText reads a value as text: a string, a number or a boolean as written.
func valueText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return ""
	}
}
