package supportchannels

import (
	"testing"
	"time"
)

func TestSubscriberBackoffGrowsWithJitterUpToFiveMinutes(t *testing.T) {
	for n := 0; n < 40; n++ {
		want := min(subscriberRetryBase<<min(n, 7), subscriberRetryMax)
		for range 50 {
			got := subscriberBackoff(n)
			if got < want/2 || got > want {
				t.Fatalf("attempt %d: %v outside [%v, %v]", n, got, want/2, want)
			}
		}
	}
	if subscriberBackoff(100) > 5*time.Minute {
		t.Fatal("backoff exceeded its cap")
	}
}
