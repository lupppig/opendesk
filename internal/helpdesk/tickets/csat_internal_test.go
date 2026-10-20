package tickets

import "testing"

func TestCSATURL(t *testing.T) {
	for _, app := range []string{"https://app.example.com", "https://app.example.com/"} {
		if got := CSATURL(app, "tok"); got != "https://app.example.com/csat#t=tok&score=" {
			t.Fatalf("CSATURL(%q) = %q", app, got)
		}
	}
}
