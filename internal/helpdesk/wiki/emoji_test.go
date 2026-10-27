package wiki

import (
	"os"
	"regexp"
	"testing"
)

func TestIsEmoji(t *testing.T) {
	for s, want := range map[string]bool{
		"\U0001F4D8":           true,
		"\u2611\uFE0F":         true, // with its variation selector
		"\U0001F1ED\U0001F1FA": true, // regional indicator pair
		"\U0001F468\u200D\U0001F469\u200D\U0001F467\u200D\U0001F466": true, // ZWJ family
		"\U0001F44B\U0001F3FD":                 true, // skin tone
		"\U0001F9D1\U0001F3FF\u200D\U0001F4BB": true, // ZWJ with a skin tone
		"\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F": true, // tag sequence
		"1\uFE0F\u20E3":        true, // keycap
		"#\u20E3":              true,
		"\u202E\U0001F4D8":     false, // bidi override
		"\U0001F4D8\u202E":     false,
		"\u200B\U0001F4D8":     false, // zero-width space
		"\U0001F4D8\u0301":     false, // combining mark
		"\U0001F4D8\u200D":     false, // dangling joiner
		"\U0001F4D8\U0001F4D8": false, // two emoji
		"\U0001F1ED":           false, // half a flag
		"a":                    false,
		"\u05D0":               false,
		"1":                    false,
		"\u20E3":               false,
		"\U0001F3F4\U000E0065": false, // tags without the cancel tag
	} {
		if got := isEmoji(s); got != want {
			t.Errorf("isEmoji(%+q) = %v", s, got)
		}
	}
}

// Every icon the web app's picker offers is one the API takes.
func TestPickerEmoji(t *testing.T) {
	src, err := os.ReadFile("../../../web/src/components/wiki/emoji.ts")
	if err != nil {
		t.Skip("web sources not here")
	}
	found := regexp.MustCompile(`\["([^"]+)", "`).FindAllStringSubmatch(string(src), -1)
	if len(found) < 100 {
		t.Fatalf("read %d emoji", len(found))
	}
	for _, m := range found {
		if !isEmoji(m[1]) {
			t.Errorf("the picker offers %+q, which the API refuses", m[1])
		}
	}
}
