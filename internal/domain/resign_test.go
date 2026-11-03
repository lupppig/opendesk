package domain

import (
	"html"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestResignAttachmentLinks(t *testing.T) {
	Configure("https://api.example.com", []byte(strings.Repeat("k", 32)))
	id := uuid.New()
	in := `<img src="https://api.example.com/v1/attachments/` + id.String() + `?exp=1&amp;sig=00">`
	out := ResignAttachmentLinks(in, []Attachment{{ID: id}})
	if out == in || !strings.Contains(out, "&amp;sig=") {
		t.Fatalf("not re-signed: %s", out)
	}
	src := strings.TrimSuffix(strings.TrimPrefix(out, `<img src="https://api.example.com/v1/attachments/`+id.String()+`?exp=`), `">`)
	exp, sig, _ := strings.Cut(src, "&amp;sig=")
	if !VerifyAttachmentSig(id, exp, sig, false) {
		t.Fatalf("fresh link does not verify: %s", out)
	}
}

func TestResignAttachmentLinksOnlyOwn(t *testing.T) {
	Configure("https://api.example.com", []byte(strings.Repeat("k", 32)))
	other := uuid.New()
	in := `<img src="https://api.example.com/v1/attachments/` + other.String() + `?exp=1&amp;sig=00">`
	if out := ResignAttachmentLinks(in, []Attachment{{ID: uuid.New()}}); out != in {
		t.Fatalf("signed a link to another message's file: %s", out)
	}
}

// TestResignAttachmentLinksAfterMove: a link stored while the instance had another address
// (API_URL) gets the current one; a link stored without an origin keeps none.
func TestResignAttachmentLinksAfterMove(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	Configure("https://old-desk.example.com", secret)
	id := uuid.New()
	stored := AttachmentURL(id)
	Configure("https://desk.example.com", secret)
	for _, in := range []string{
		`<img src="` + strings.Replace(stored, "&", "&amp;", 1) + `">`,
		`<img src="` + stored + `">`,
		`<img src="http://old-desk.example.com:8080/v1/attachments/` + id.String() + `?exp=1&amp;sig=00">`,
	} {
		out := ResignAttachmentLinks(in, []Attachment{{ID: id}})
		if strings.Contains(out, "old-desk") || !strings.HasPrefix(out, `<img src="https://desk.example.com/v1/attachments/`+id.String()+`?exp=`) {
			t.Fatalf("not at the current address: %s", out)
		}
		u, err := url.Parse(html.UnescapeString(strings.TrimSuffix(strings.TrimPrefix(out, `<img src="`), `">`)))
		if err != nil || !VerifyAttachmentSig(id, u.Query().Get("exp"), u.Query().Get("sig"), false) {
			t.Fatalf("fresh link does not verify: %s", out)
		}
	}
	rel := `<img src="/v1/attachments/` + id.String() + `?exp=1&amp;sig=00">`
	if out := ResignAttachmentLinks(rel, []Attachment{{ID: id}}); !strings.HasPrefix(out, `<img src="/v1/attachments/`+id.String()+`?exp=`) || out == rel {
		t.Fatalf("relative link: %s", out)
	}
	// Another site's image, and another message's file at the old address, are left alone.
	for _, in := range []string{
		`<img src="https://cdn.example.org/logo.png">`,
		`<img src="https://old-desk.example.com/v1/attachments/` + uuid.NewString() + `?exp=1&amp;sig=00">`,
	} {
		if out := ResignAttachmentLinks(in, []Attachment{{ID: id}}); out != in {
			t.Fatalf("rewrote a link that is not the message's own: %s", out)
		}
	}
}

// TestDeletedAttachmentLinks checks a deleted message's files are signed apart: a link
// handed out before it was deleted does not open them, and theirs opens nothing else.
func TestDeletedAttachmentLinks(t *testing.T) {
	Configure("https://api.example.com", []byte(strings.Repeat("k", 32)))
	id := uuid.New()
	parts := func(u string) (string, string) {
		q := strings.SplitN(u, "?exp=", 2)[1]
		exp, sig, _ := strings.Cut(q, "&sig=")
		return exp, sig
	}
	exp, sig := parts(AttachmentURL(id))
	if !VerifyAttachmentSig(id, exp, sig, false) || VerifyAttachmentSig(id, exp, sig, true) {
		t.Fatal("a plain link must open only a file of a message that is not deleted")
	}
	exp, sig = parts(DeletedAttachmentURL(id))
	if !VerifyAttachmentSig(id, exp, sig, true) || VerifyAttachmentSig(id, exp, sig, false) {
		t.Fatal("a deleted message's link must open only that file while it is deleted")
	}
}
