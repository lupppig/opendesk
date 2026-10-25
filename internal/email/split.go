package email

// SplitQuoted separates what a reply's author wrote from the quoted history and signature
// under it, exactly as an inbound email's body is split. html is the raw body (not yet
// sanitized; "" for a plain-text email) and text its plain-text part. visible and plain
// are what the ticket shows; full is the whole original, sanitized, or "" when nothing
// was cut. Imports use it so an imported reply reads like a live one.
func SplitQuoted(html, text, subject string) (visible, full, plain string) {
	b := renderBody(&parsedMail{HTML: html, Text: text, Subject: subject}, nil)
	return b.html, b.full, b.text
}
