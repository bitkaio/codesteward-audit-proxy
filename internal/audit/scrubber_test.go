package audit

import (
	"testing"
)

func TestPatternScrubber_RedactsEmail(t *testing.T) {
	s, err := NewPatternScrubber([]string{`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := s.Scrub("contact me at user@example.com please")
	want := "contact me at [REDACTED] please"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPatternScrubber_RedactsAPIKey(t *testing.T) {
	s, err := NewPatternScrubber([]string{`sk-[a-zA-Z0-9]{32,}`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := s.Scrub("my key is sk-abcdefghijklmnopqrstuvwxyz123456 done")
	want := "my key is [REDACTED] done"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPatternScrubber_MultiplePatterns(t *testing.T) {
	s, err := NewPatternScrubber([]string{
		`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`,
		`sk-[a-zA-Z0-9]{32,}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	input := "email: dev@example.com key: sk-abcdefghijklmnopqrstuvwxyz123456"
	got := s.Scrub(input)
	want := "email: [REDACTED] key: [REDACTED]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPatternScrubber_NoMatchPassesThrough(t *testing.T) {
	s, err := NewPatternScrubber([]string{`sk-[a-zA-Z0-9]{32,}`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	input := "nothing sensitive here"
	got := s.Scrub(input)
	if got != input {
		t.Errorf("expected unchanged output, got %q", got)
	}
}

func TestPatternScrubber_InvalidPatternErrors(t *testing.T) {
	_, err := NewPatternScrubber([]string{`[invalid`})
	if err == nil {
		t.Fatal("expected error for invalid pattern, got nil")
	}
}

func TestNopScrubber_PassesThrough(t *testing.T) {
	s := NopScrubber{}

	cases := []string{
		"",
		"hello world",
		"user@example.com",
		"sk-abcdefghijklmnopqrstuvwxyz123456",
	}
	for _, input := range cases {
		if got := s.Scrub(input); got != input {
			t.Errorf("NopScrubber.Scrub(%q) = %q, want unchanged", input, got)
		}
	}
}

func TestPatternScrubber_EmptyPatternsSlice(t *testing.T) {
	s, err := NewPatternScrubber([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	input := "unchanged content"
	if got := s.Scrub(input); got != input {
		t.Errorf("got %q, want %q", got, input)
	}
}

func TestPatternScrubber_SkipsBlanks(t *testing.T) {
	// Comma-split of "pat1,,pat2" produces an empty string in the middle.
	s, err := NewPatternScrubber([]string{"sk-[a-zA-Z0-9]{32,}", "", "  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := s.Scrub("key: sk-abcdefghijklmnopqrstuvwxyz123456")
	want := "key: [REDACTED]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
