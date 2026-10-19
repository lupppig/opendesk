package collab

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Gmail's settings (settings.go): the sendAs entry of the account's address carries its
// signature and From name (users.settings.sendAs), and the vacation responder its automatic
// reply (users.settings.getVacation / updateVacation). gmail.modify reads both; changing
// them needs gmail.settings.basic.
//
// Automatic reply mapping (Gmail VacationSettings):
//   enabled       enableAutoReply
//   start, end    startTime, endTime (epoch milliseconds, as strings); either may be absent
//   subject       responseSubject
//   message_html  responseBodyHtml (responseBodyPlainText is read when there is no HTML)
//   contacts_only restrictToContacts: replies only to people in the account's contacts
//   internal_only restrictToDomain: replies only to people in the account's Google
//                 Workspace domain (Workspace accounts only; Gmail refuses it elsewhere)

// gmailSendAs is a users.settings.sendAs resource (the fields used).
type gmailSendAs struct {
	SendAsEmail string `json:"sendAsEmail"`
	DisplayName string `json:"displayName"`
	Signature   string `json:"signature"`
	IsPrimary   bool   `json:"isPrimary"`
}

// sendAs reads the account address's sendAs entry: the address itself, else (when Gmail
// does not know it as one) the one matching it in the list, else the primary one.
func (g *gmailProvider) sendAs(ctx context.Context) (gmailSendAs, error) {
	var one gmailSendAs
	err := g.do(ctx, http.MethodGet, "/settings/sendAs/"+url.PathEscape(g.a.Email), nil, &one)
	if err == nil {
		return one, nil
	}
	if !isNotFound(err) {
		return one, err
	}
	var list struct {
		SendAs []gmailSendAs `json:"sendAs"`
	}
	if err := g.do(ctx, http.MethodGet, "/settings/sendAs", nil, &list); err != nil {
		return one, err
	}
	var primary *gmailSendAs
	for i, sa := range list.SendAs {
		if strings.EqualFold(sa.SendAsEmail, g.a.Email) {
			return sa, nil
		}
		if sa.IsPrimary {
			primary = &list.SendAs[i]
		}
	}
	if primary == nil {
		return one, &providerError{msg: "Gmail has no send-as address for this account.", permanent: true}
	}
	return *primary, nil
}

// patchSendAs changes the account's sendAs entry (gmail.settings.basic) and returns
// Gmail's answer.
func (g *gmailProvider) patchSendAs(ctx context.Context, body map[string]string) (gmailSendAs, error) {
	sa, err := g.sendAs(ctx)
	if err != nil {
		return sa, err
	}
	var out gmailSendAs
	err = g.do(ctx, http.MethodPatch, "/settings/sendAs/"+url.PathEscape(sa.SendAsEmail), body, &out)
	return out, err
}

// gmailVacation is users.settings.vacation (startTime and endTime are int64 strings).
type gmailVacation struct {
	EnableAutoReply       bool        `json:"enableAutoReply"`
	ResponseSubject       string      `json:"responseSubject,omitempty"`
	ResponseBodyPlainText string      `json:"responseBodyPlainText,omitempty"`
	ResponseBodyHTML      string      `json:"responseBodyHtml,omitempty"`
	RestrictToContacts    bool        `json:"restrictToContacts"`
	RestrictToDomain      bool        `json:"restrictToDomain"`
	StartTime             json.Number `json:"startTime,omitempty"`
	EndTime               json.Number `json:"endTime,omitempty"`
}

// gmailMillis reads an epoch-milliseconds value; nil when absent or zero.
func gmailMillis(n json.Number) *time.Time {
	ms, err := strconv.ParseInt(strings.Trim(string(n), `"`), 10, 64)
	if err != nil || ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func millisOf(t *time.Time) json.Number {
	if t == nil {
		return ""
	}
	return json.Number(strconv.FormatInt(t.UnixMilli(), 10))
}

// outOfOffice maps the vacation responder; the message is not sanitized yet.
func (v gmailVacation) outOfOffice() OutOfOffice {
	msg := v.ResponseBodyHTML
	if msg == "" && v.ResponseBodyPlainText != "" {
		msg = "<p>" + strings.ReplaceAll(html.EscapeString(v.ResponseBodyPlainText), "\n", "<br>") + "</p>"
	}
	return OutOfOffice{Enabled: v.EnableAutoReply, Start: gmailMillis(v.StartTime), End: gmailMillis(v.EndTime),
		Subject: v.ResponseSubject, MessageHTML: msg, ContactsOnly: v.RestrictToContacts, InternalOnly: v.RestrictToDomain}
}

// hash compares what Gmail keeps, whatever order or extra fields its answer has.
func (v gmailVacation) hash() string {
	v.StartTime, v.EndTime = millisOf(gmailMillis(v.StartTime)), millisOf(gmailMillis(v.EndTime))
	return settingHash(v)
}

func (g *gmailProvider) vacation(ctx context.Context) (gmailVacation, error) {
	var v gmailVacation
	err := g.do(ctx, http.MethodGet, "/settings/vacation", nil, &v)
	return v, err
}

// putVacation replaces the vacation responder (gmail.settings.basic) and returns Gmail's
// answer. The message is sanitized by the caller.
func (g *gmailProvider) putVacation(ctx context.Context, o OutOfOffice) (gmailVacation, error) {
	in := gmailVacation{EnableAutoReply: o.Enabled, ResponseSubject: o.Subject, ResponseBodyHTML: o.MessageHTML,
		RestrictToContacts: o.ContactsOnly, RestrictToDomain: o.InternalOnly, StartTime: millisOf(o.Start), EndTime: millisOf(o.End)}
	var out gmailVacation
	err := g.do(ctx, http.MethodPut, "/settings/vacation", in, &out)
	return out, err
}

// readSettings reads the signature, From name and automatic reply. What was read is kept
// even when another part failed.
func (g *gmailProvider) readSettings(ctx context.Context) (remoteSettings, error) {
	var r remoteSettings
	sa, err := g.sendAs(ctx)
	if err == nil {
		r.signature, r.displayName = &sa.Signature, &sa.DisplayName
	}
	v, verr := g.vacation(ctx)
	if verr == nil {
		ooo := v.outOfOffice()
		r.ooo, r.oooHash = &ooo, v.hash()
	} else if err == nil {
		err = verr
	}
	return r, err
}
