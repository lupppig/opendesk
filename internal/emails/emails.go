// Package emails builds every platform email opendesk sends: account emails (confirm,
// reset, invite), billing emails and notification emails. Each builder is a pure
// function of its input, so cmd/emailpreview renders all of them with sample data, and
// all of them share the layout in internal/mailer.
package emails

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/mailer"
)

// Contact is opendesk's one contact address, shown in Cloud emails.
const Contact = "team@opendesk.sh"

// Site is what emails need from the instance.
type Site struct {
	AppURL string
	APIURL string
	// Contact is an address for questions in footers; empty on self-hosted instances,
	// whose people ask their own admins.
	Contact string
	// Legal is the sending company's identification line (config.EmailLegal); empty
	// leaves it out, so an instance never names a company its operator did not set.
	Legal string
}

// SiteFrom reads the instance's site. The Cloud (billing on) shows opendesk's contact.
func SiteFrom(c *config.Config) Site {
	s := Site{AppURL: c.AppURL, APIURL: c.APIURL, Legal: c.EmailLegal.Line()}
	if c.BillingEnabled() {
		s.Contact = Contact
	}
	return s
}

// App is an app link with optional query parameters, given as key, value pairs.
func (s Site) App(path string, kv ...string) string {
	u := strings.TrimRight(s.AppURL, "/") + path
	if len(kv) >= 2 {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i+1] != "" {
				v.Set(kv[i], kv[i+1])
			}
		}
		if q := v.Encode(); q != "" {
			u += "?" + q
		}
	}
	return u
}

// build renders e with the instance's footer additions.
func (s Site) build(e mailer.Email) mailer.Mail {
	e.Footer.Legal = s.Legal
	html, text := mailer.Render(e)
	return mailer.Mail{Subject: e.Subject, HTML: html, Text: text}
}

// VerifyEmail asks a new account to confirm its address.
func VerifyEmail(s Site, link string) mailer.Mail {
	return s.build(mailer.Email{
		Subject:   "Confirm your email",
		Preheader: "One click to finish setting up your opendesk account.",
		Heading:   "Confirm your email",
		Intro:     []string{"Confirm this address to finish setting up your opendesk account. It is where opendesk sends sign-in help and notification emails."},
		Button:    &mailer.Button{Label: "Confirm email", URL: link},
		Outro:     []string{"The link works for 48 hours."},
		Footer: mailer.Footer{
			Reason:  "You get this email because this address was used to sign up for opendesk. If that was not you, ignore it and nothing happens.",
			Contact: s.Contact,
		},
	})
}

// ResetPassword sends a password reset link.
func ResetPassword(s Site, link string) mailer.Mail {
	return s.build(mailer.Email{
		Subject:   "Reset your opendesk password",
		Preheader: "Choose a new password with the link inside. It works for one hour.",
		Heading:   "Reset your password",
		Intro:     []string{"Someone asked to reset the password for your opendesk account. Choose a new one with the button below."},
		Button:    &mailer.Button{Label: "Choose a new password", URL: link},
		Outro:     []string{"The link works for one hour and only once. Choosing a new password signs you out on your other devices."},
		Footer: mailer.Footer{
			Reason:  "You get this email because a password reset was asked for this address. If it was not you, ignore it: your password stays the same.",
			Contact: s.Contact,
		},
	})
}

// Invite asks someone to join a workspace.
type Invite struct {
	Inviter   string
	Workspace string
	Role      string
	Link      string
}

// Invitation builds the invite email.
func Invitation(s Site, in Invite) mailer.Mail {
	inviter := in.Inviter
	if inviter == "" {
		inviter = "A teammate"
	}
	role := roleLabel(in.Role)
	return s.build(mailer.Email{
		Subject:   "You are invited to " + in.Workspace + " on opendesk",
		Preheader: inviter + " invited you to answer customers together in " + in.Workspace + ".",
		Eyebrow:   in.Workspace,
		Heading:   "Join " + in.Workspace + " on opendesk",
		Intro:     []string{fmt.Sprintf("%s invited you to the %s workspace as %s. opendesk is where the team answers customers together: shared inboxes, live chat and a help center.", inviter, in.Workspace, article(role))},
		Facts:     []mailer.Fact{{Label: "Workspace", Value: in.Workspace}, {Label: "Role", Value: capitalize(role)}, {Label: "Invited by", Value: inviter}},
		Button:    &mailer.Button{Label: "Accept invitation", URL: in.Link},
		Outro:     []string{"The invitation works for 7 days. Sign in or create an account with this email address to accept it."},
		Footer: mailer.Footer{
			Reason:  "You get this email because " + inviter + " invited this address to " + in.Workspace + ". If you were not expecting it, ignore it.",
			Contact: s.Contact,
		},
	})
}

// TwoFactorDisabled tells a person an instance administrator turned off their two-factor
// authentication (contracts/admin.md), so they notice if they did not ask for it.
func TwoFactorDisabled(s Site) mailer.Mail {
	return s.build(mailer.Email{
		Subject:   "Two-factor authentication was turned off",
		Preheader: "An administrator turned it off for your opendesk account. Turn it back on in your security settings.",
		Heading:   "Two-factor authentication is off",
		Intro: []string{"An administrator of this opendesk instance turned off two-factor authentication for your account and signed you out everywhere.",
			"This usually happens after you lost your authenticator and asked for help. Sign in and turn it back on to keep your account protected."},
		Button: &mailer.Button{Label: "Open security settings", URL: s.App("/settings/security")},
		Footer: mailer.Footer{
			Reason:  "You get this email because the security settings of your opendesk account changed. If you did not ask for this, change your password and contact support.",
			Contact: s.Contact,
		},
	})
}

// SecurityChange is a change to how someone signs in, which they are told about by email
// (ASVS 2.2.3, 2.5.5): if they did not make it, the email is how they find out.
type SecurityChange string

const (
	PasswordChanged    SecurityChange = "password_changed"
	PasswordReset      SecurityChange = "password_reset"
	TwoFactorEnabled   SecurityChange = "totp_enabled"
	TwoFactorTurnedOff SecurityChange = "totp_disabled"
	RecoveryCodesNew   SecurityChange = "recovery_codes"
	PasskeyAdded       SecurityChange = "passkey_added"
	PasskeyRemoved     SecurityChange = "passkey_removed"
	ProviderLinked     SecurityChange = "provider_linked"
)

// securityChanges are each change's subject and first line.
var securityChanges = map[SecurityChange][2]string{
	PasswordChanged:    {"Your password was changed", "The password of your opendesk account was changed, and every other session was signed out."},
	PasswordReset:      {"Your password was reset", "The password of your opendesk account was reset from a link sent to this address, and every session was signed out."},
	TwoFactorEnabled:   {"Two-factor authentication was turned on", "Two-factor authentication was turned on for your opendesk account. Signing in now also asks for a code from your authenticator app."},
	TwoFactorTurnedOff: {"Two-factor authentication was turned off", "Two-factor authentication was turned off for your opendesk account. Signing in now asks for your password only."},
	RecoveryCodesNew:   {"New recovery codes were made", "New two-factor recovery codes were made for your opendesk account. The old ones no longer work."},
	PasskeyAdded:       {"A passkey was added", "A passkey was added to your opendesk account. It can now be used to sign in."},
	PasskeyRemoved:     {"A passkey was removed", "A passkey was removed from your opendesk account. It can no longer be used to sign in."},
	ProviderLinked:     {"A sign-in method was added", "A new way to sign in was linked to your opendesk account."},
}

// SecurityNotice tells a person their sign-in settings changed. detail names what changed
// when there is more to say (a passkey's name, the provider linked); it may be empty.
func SecurityNotice(s Site, change SecurityChange, detail string) mailer.Mail {
	text, ok := securityChanges[change]
	if !ok {
		text = [2]string{"Your account's security settings changed", "The security settings of your opendesk account changed."}
	}
	intro := []string{text[1]}
	if detail != "" {
		intro = append(intro, detail)
	}
	intro = append(intro, "If this was you, there is nothing to do. If it was not, reset your password now and check your security settings.")
	return s.build(mailer.Email{
		Subject:   text[0],
		Preheader: text[1],
		Heading:   text[0],
		Intro:     intro,
		Button:    &mailer.Button{Label: "Open security settings", URL: s.App("/settings/security")},
		Outro:     []string{"Not you? Reset your password at " + s.App("/forgot") + "."},
		Footer: mailer.Footer{
			Reason:  "You get this email because the security settings of your opendesk account changed. We always send it, so you know about every change.",
			Contact: s.Contact,
		},
	})
}

// SignupAttempt tells the owner of an address that someone tried to create an account
// with it, so a probe for who has an account does not go unnoticed.
func SignupAttempt(s Site) mailer.Mail {
	return s.build(mailer.Email{
		Subject:   "Someone tried to sign up with your email",
		Preheader: "Your opendesk account already uses this address, so no new account was made.",
		Heading:   "You already have an account",
		Intro: []string{"Someone tried to create an opendesk account with this email address. You already have one, so nothing changed.",
			"If it was you, sign in instead, or reset your password if you forgot it. If it was not, there is nothing to do."},
		Button: &mailer.Button{Label: "Sign in", URL: s.App("/login")},
		Outro:  []string{"Forgot your password? Reset it at " + s.App("/forgot") + "."},
		Footer: mailer.Footer{
			Reason:  "You get this email because someone entered this address to create an opendesk account.",
			Contact: s.Contact,
		},
	})
}

// PaymentFailed tells owners and admins a subscription payment failed.
func PaymentFailed(s Site, workspace string, workspaceID string) mailer.Mail {
	return s.build(mailer.Email{
		Subject:   "Payment failed for " + workspace,
		Preheader: "Update the payment method to keep Pro. Stripe retries over the next few days.",
		Eyebrow:   workspace,
		Heading:   "We could not collect your payment",
		Intro: []string{"The latest payment for the opendesk Pro subscription of " + workspace + " failed.",
			"Stripe retries over the next few days and the workspace stays on Pro meanwhile. Update the payment method to keep it that way."},
		Button: &mailer.Button{Label: "Update payment method", URL: s.App("/settings/billing", "ws", workspaceID)},
		Footer: mailer.Footer{
			Reason:  "You get this email because you are an owner or admin of " + workspace + ".",
			Contact: s.Contact,
		},
	})
}

func roleLabel(role string) string {
	switch role {
	case "owner", "admin", "agent", "viewer":
		return role
	}
	return "agent"
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an " + word
	}
	return "a " + word
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// UpdateAvailable tells an instance's super admins a newer opendesk release is out. major
// marks a new major version, which automatic updates leave to the operator.
func UpdateAvailable(s Site, adminLink, current, latest, releaseURL string, major bool) mailer.Mail {
	how := "Upgrade on the server with: opendesk-ctl upgrade " + latest + ". It backs up the instance first."
	if major {
		how = "It is a new major version, so automatic updates leave it to you. Read the release notes, then upgrade on the server with: opendesk-ctl upgrade " + latest + "."
	}
	return s.build(mailer.Email{
		Subject:   "opendesk " + latest + " is available",
		Preheader: "This instance runs " + current + ". " + how,
		Heading:   "opendesk " + latest + " is available",
		Intro:     []string{"A new opendesk release is out. This instance runs " + current + ".", how},
		Facts:     []mailer.Fact{{Label: "Running", Value: current}, {Label: "Newest release", Value: latest}},
		Button:    &mailer.Button{Label: "Read the release notes", URL: releaseURL},
		Outro:     []string{"The admin panel shows the version and update status under Health: " + adminLink},
		Footer: mailer.Footer{
			Reason:  "You get this email because you are a super admin of this opendesk instance. Set UPDATE_CHECK=false to stop looking for releases.",
			Contact: s.Contact,
		},
	})
}

// AutoUpdateFailed tells an instance's super admins an automatic update did not finish and
// the instance still runs the version it ran before. detail is opendesk-ctl's line about it.
func AutoUpdateFailed(s Site, adminLink, detail string) mailer.Mail {
	return s.build(mailer.Email{
		Subject:   "An automatic opendesk update failed",
		Preheader: "The instance runs the version it ran before.",
		Heading:   "An automatic update failed",
		Intro: []string{"opendesk-ctl tried to install a new release on this instance and it did not finish, so the instance runs the version it ran before.",
			detail,
			"To see why, run opendesk-ctl auto-update status and opendesk-ctl logs opendesk on the server."},
		Button: &mailer.Button{Label: "Open the admin panel", URL: adminLink},
		Footer: mailer.Footer{
			Reason:  "You get this email because you are a super admin of this opendesk instance.",
			Contact: s.Contact,
		},
	})
}
