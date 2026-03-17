package audit

import (
	"fmt"
	"regexp"
	"strings"
)

// Scrubber transforms a string before it is stored in an audit record.
// Implementations must be safe for concurrent use.
type Scrubber interface {
	Scrub(content string) string
}

// NopScrubber passes content through unchanged. Used when
// AUDIT_CAPTURE_REQUESTS=true and no patterns are configured.
type NopScrubber struct{}

func (NopScrubber) Scrub(content string) string { return content }

// PatternScrubber replaces all regexp matches with [REDACTED].
// Patterns are compiled once at construction time; Scrub is safe for
// concurrent use.
type PatternScrubber struct {
	patterns []*regexp.Regexp
}

// NewPatternScrubber compiles each pattern and returns an error identifying
// the first invalid pattern. Call this once at startup.
func NewPatternScrubber(rawPatterns []string) (*PatternScrubber, error) {
	compiled := make([]*regexp.Regexp, 0, len(rawPatterns))
	for _, p := range rawPatterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		r, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("audit: invalid scrub pattern %q: %w", p, err)
		}
		compiled = append(compiled, r)
	}
	return &PatternScrubber{patterns: compiled}, nil
}

// Scrub applies every pattern in order, replacing matches with [REDACTED].
func (s *PatternScrubber) Scrub(content string) string {
	for _, r := range s.patterns {
		content = r.ReplaceAllString(content, "[REDACTED]")
	}
	return content
}
