package source

import "testing"

func TestClientURLRejectsSchemeDowngrade(t *testing.T) {
	c := NewClient("https://api.example.test", Options{})

	if _, err := c.URL("http://api.example.test/tickets?page=2", nil); err == nil {
		t.Fatal("URL accepted an HTTP link for an HTTPS API")
	}
}
