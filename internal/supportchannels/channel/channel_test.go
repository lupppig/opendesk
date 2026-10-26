package channel

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimitDelay(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		provider float64
		want     time.Duration
	}{
		{name: "fallback", want: time.Minute},
		{name: "header seconds", header: "7.5", want: 7500 * time.Millisecond},
		{name: "zero header uses minimum", header: "0", want: time.Second},
		{name: "maximum", header: "9000", want: 15 * time.Minute},
		{name: "provider value", header: "9", provider: 2.5, want: 2500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := make(http.Header)
			if tt.header != "" {
				header.Set("Retry-After", tt.header)
			}
			if got := RateLimitDelay(header, tt.provider); got != tt.want {
				t.Fatalf("RateLimitDelay() = %s, want %s", got, tt.want)
			}
		})
	}
}
