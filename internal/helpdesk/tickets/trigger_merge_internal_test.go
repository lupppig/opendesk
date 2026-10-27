package tickets

import "testing"

func TestSameSubject(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Order 1042 not delivered", "Order 1042 not delivered", true},
		{"Order 1042 not delivered", "RE:  order 1042   NOT delivered", true},
		{"Order 1042 not delivered [#41]", "Re: Fwd: Order 1042 not delivered", true},
		{"Commande perdue", "TR : RE : Commande perdue", true},
		{"Bestellung fehlt", "AW: WG: Bestellung fehlt", true},
		{"Order 1042 not delivered", "Order 1043 not delivered", false},
		{"Order not delivered", "Re: Order not delivered again", false},
		// No subject says nothing about the conversation.
		{"(no subject)", "(no subject)", false},
		{"", "Re:", false},
	}
	for _, c := range cases {
		if got := sameSubject(c.a, c.b); got != c.want {
			t.Errorf("sameSubject(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
