package hd

import (
	"strings"
	"testing"
)

// A signature as Gmail's sendAs returns it: a ltr div, a layout table, a sized logo,
// lines split by <br> and divs, inline styles.
const gmailSignature = `<div dir="ltr"><div>Thanks,</div><table cellpadding="0" cellspacing="0" border="0" style="border-collapse:collapse;font-family:Arial,sans-serif"><tbody><tr>` +
	`<td valign="top" style="padding-right:12px"><img src="https://ci3.googleusercontent.com/mail-sig/AIorK4demo" width="96" height="38" alt="Northwind"></td>` +
	`<td valign="top" style="border-left:2px solid #e5e7eb;padding-left:12px"><b>Ada Lovelace</b><br><span style="color:#6b7280;font-size:12px">Founder &amp; CEO</span>` +
	`<div><a href="mailto:ada@northwind.example">ada@northwind.example</a> · <a href="https://northwind.example">northwind.example</a></div></td>` +
	`</tr></tbody></table><div style="color:#9ca3af;font-size:11px">Northwind Labs Ltd, registered in England No. 00000000</div></div>`

// The layout people see in Gmail is what storage and sending keep.
func TestSignatureLayoutSurvives(t *testing.T) {
	keep := []string{
		`<div dir="ltr">`, `<table`, `cellpadding="0"`, `border-collapse: collapse`, `<td valign="top"`,
		`width="96"`, `height="38"`, `<b>Ada Lovelace</b><br><span`, `Founder &amp; CEO`, `border-left: 2px solid #e5e7eb`,
		`href="mailto:ada@northwind.example"`, `font-size: 11px`,
	}
	stored := SanitizeSignature(gmailSignature)
	for _, k := range keep {
		if !strings.Contains(stored, k) {
			t.Errorf("stored signature lost %q:\n%s", k, stored)
		}
	}

	// The composer wraps the stored signature in its block; drafts and sent mail are
	// cleaned with the email policy, which keeps the block's class and the layout.
	body := `<p>Hi there</p><p></p><div class="od-signature">` + stored + `</div>`
	sent := SanitizeEmailHTML(body)
	for _, k := range append(keep, `<div class="od-signature">`) {
		if !strings.Contains(sent, k) {
			t.Errorf("sent body lost %q:\n%s", k, sent)
		}
	}
	// Cleaning what was already cleaned changes nothing: a draft saved and opened again
	// carries the same signature.
	if again := SanitizeEmailHTML(sent); again != sent {
		t.Errorf("a saved draft changes on the next save:\n%s\n%s", sent, again)
	}
}
