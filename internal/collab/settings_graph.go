package collab

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Microsoft's settings (settings.go). Graph has no signature API, so a Microsoft account's
// signature lives in opendesk. Its From name is read from the profile (/me displayName,
// User.Read) until a person changes it in opendesk. Its automatic reply is the mailbox
// settings' automaticRepliesSetting (MailboxSettings.ReadWrite).
//
// Automatic reply mapping (Graph automaticRepliesSetting):
//   enabled       status: disabled, else alwaysEnabled, or scheduled when both times are set
//   start, end    scheduledStartDateTime, scheduledEndDateTime (written in UTC); read only
//                 while the status is scheduled, and Outlook needs both
//   subject       none: Outlook's replies have no subject of their own (always "")
//   message_html  internalReplyMessage and externalReplyMessage, the same message for both
//                 (read: the internal one, else the external one)
//   internal_only externalAudience none: people outside the organization get no reply
//   contacts_only externalAudience contactsOnly: outside the organization, only contacts
//                 get one (people inside it always do while it is on)
//   neither       externalAudience all

// graphDateTime is Graph's dateTimeTimeZone.
type graphDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// graphAutoReplies is automaticRepliesSetting.
type graphAutoReplies struct {
	Status                 string         `json:"status"`
	ExternalAudience       string         `json:"externalAudience"`
	ScheduledStartDateTime *graphDateTime `json:"scheduledStartDateTime,omitempty"`
	ScheduledEndDateTime   *graphDateTime `json:"scheduledEndDateTime,omitempty"`
	InternalReplyMessage   string         `json:"internalReplyMessage"`
	ExternalReplyMessage   string         `json:"externalReplyMessage"`
}

// windowsZones maps the Windows time zone names Outlook commonly uses to IANA names; a
// name Go cannot load and that is not here is read as UTC.
var windowsZones = map[string]string{
	"UTC": "UTC", "Coordinated Universal Time": "UTC", "GMT Standard Time": "Europe/London",
	"Greenwich Standard Time": "Atlantic/Reykjavik", "W. Europe Standard Time": "Europe/Berlin",
	"Central Europe Standard Time": "Europe/Budapest", "Romance Standard Time": "Europe/Paris",
	"Central European Standard Time": "Europe/Warsaw", "E. Europe Standard Time": "Europe/Chisinau",
	"FLE Standard Time": "Europe/Kiev", "GTB Standard Time": "Europe/Bucharest", "Russian Standard Time": "Europe/Moscow",
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "US Mountain Standard Time": "America/Phoenix",
	"Pacific Standard Time": "America/Los_Angeles", "Alaskan Standard Time": "America/Anchorage",
	"Hawaiian Standard Time": "Pacific/Honolulu", "Atlantic Standard Time": "America/Halifax",
	"E. South America Standard Time": "America/Sao_Paulo", "India Standard Time": "Asia/Kolkata",
	"China Standard Time": "Asia/Shanghai", "Tokyo Standard Time": "Asia/Tokyo", "Korea Standard Time": "Asia/Seoul",
	"Singapore Standard Time": "Asia/Singapore", "Arabian Standard Time": "Asia/Dubai", "Israel Standard Time": "Asia/Jerusalem",
	"South Africa Standard Time": "Africa/Johannesburg", "AUS Eastern Standard Time": "Australia/Sydney",
	"E. Australia Standard Time": "Australia/Brisbane", "W. Australia Standard Time": "Australia/Perth",
	"New Zealand Standard Time": "Pacific/Auckland",
}

// at reads a dateTimeTimeZone as a time; nil when absent or unreadable.
func (d *graphDateTime) at() *time.Time {
	if d == nil || d.DateTime == "" {
		return nil
	}
	loc := time.UTC
	if name := strings.TrimSpace(d.TimeZone); name != "" {
		if iana, ok := windowsZones[name]; ok {
			name = iana
		}
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.9999999", "2006-01-02T15:04:05", time.RFC3339Nano} {
		if t, err := time.ParseInLocation(layout, d.DateTime, loc); err == nil {
			t = t.UTC().Truncate(time.Second)
			return &t
		}
	}
	return nil
}

func graphDateTimeOf(t *time.Time) *graphDateTime {
	if t == nil {
		return nil
	}
	return &graphDateTime{DateTime: t.UTC().Format("2006-01-02T15:04:05"), TimeZone: "UTC"}
}

// outOfOffice maps the automatic replies; the message is not sanitized yet.
func (r graphAutoReplies) outOfOffice() OutOfOffice {
	o := OutOfOffice{Enabled: !strings.EqualFold(r.Status, "disabled") && r.Status != "", MessageHTML: r.InternalReplyMessage,
		InternalOnly: strings.EqualFold(r.ExternalAudience, "none"), ContactsOnly: strings.EqualFold(r.ExternalAudience, "contactsOnly")}
	if o.MessageHTML == "" {
		o.MessageHTML = r.ExternalReplyMessage
	}
	if strings.EqualFold(r.Status, "scheduled") {
		o.Start, o.End = r.ScheduledStartDateTime.at(), r.ScheduledEndDateTime.at()
	}
	return o
}

// hash compares what Outlook keeps that opendesk shows (times as instants).
func (r graphAutoReplies) hash() string {
	o := r.outOfOffice()
	return settingHash(struct {
		O        OutOfOffice
		External string
	}{o, r.ExternalReplyMessage})
}

func graphAutoRepliesOf(o OutOfOffice) graphAutoReplies {
	r := graphAutoReplies{Status: "disabled", ExternalAudience: "all", InternalReplyMessage: o.MessageHTML, ExternalReplyMessage: o.MessageHTML}
	switch {
	case o.Enabled && o.Start != nil && o.End != nil:
		r.Status = "scheduled"
		r.ScheduledStartDateTime, r.ScheduledEndDateTime = graphDateTimeOf(o.Start), graphDateTimeOf(o.End)
	case o.Enabled:
		r.Status = "alwaysEnabled"
	}
	switch {
	case o.InternalOnly:
		r.ExternalAudience = "none"
	case o.ContactsOnly:
		r.ExternalAudience = "contactsOnly"
	}
	return r
}

func (g *graphProvider) autoReplies(ctx context.Context) (graphAutoReplies, error) {
	var r graphAutoReplies
	err := g.do(ctx, http.MethodGet, "/me/mailboxSettings/automaticRepliesSetting", nil, &r)
	return r, err
}

// putAutoReplies writes the automatic replies (MailboxSettings.ReadWrite) and reads them
// back. The message is sanitized by the caller.
func (g *graphProvider) putAutoReplies(ctx context.Context, o OutOfOffice) (graphAutoReplies, error) {
	in := map[string]any{"automaticRepliesSetting": graphAutoRepliesOf(o)}
	if err := g.do(ctx, http.MethodPatch, "/me/mailboxSettings", in, nil); err != nil {
		return graphAutoReplies{}, err
	}
	return g.autoReplies(ctx)
}

// profileName reads the account's display name (User.Read).
func (g *graphProvider) profileName(ctx context.Context) (string, error) {
	var me struct {
		DisplayName string `json:"displayName"`
	}
	err := g.do(ctx, http.MethodGet, "/me?$select=displayName", nil, &me)
	return me.DisplayName, err
}

// readSettings reads the profile name (while a person has not changed the From name) and
// the automatic replies (with MailboxSettings). What was read is kept when another part failed.
func (g *graphProvider) readSettings(ctx context.Context) (remoteSettings, error) {
	var r remoteSettings
	var first error
	if !g.a.settings().DisplayNameEdited {
		if name, err := g.profileName(ctx); err == nil {
			r.displayName = &name
		} else {
			first = err
		}
	}
	if outOfOfficeSync(g.a) != SyncNone {
		if ar, err := g.autoReplies(ctx); err == nil {
			ooo := ar.outOfOffice()
			r.ooo, r.oooHash = &ooo, ar.hash()
		} else if first == nil {
			first = err
		}
	}
	return r, first
}
