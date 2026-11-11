package smoke

import "testing"

func TestGreeting(t *testing.T) {
	if got := Greeting(); got != "hello" {
		t.Fatalf("Greeting() = %q, want hello", got)
	}
}
