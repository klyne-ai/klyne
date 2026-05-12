package main

import (
	"fmt"
	"strings"
)

// shortID returns the first 8 characters of an id, suitable for terminal
// tables where the full UUID is overkill.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// shortPath strips a homedir-style prefix and keeps at most the last two
// path segments so terminal tables stay readable.
func shortPath(p string) string {
	if p == "" {
		return ""
	}
	// Collapse trailing slashes.
	p = strings.TrimRight(p, "/")
	// Keep last two segments.
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// fmtCount renders an int64 token count with a thousands separator and
// optional k/M suffix when the value is large enough that the raw number
// is hard to scan. Threshold: 100_000 → 100k; 1_000_000 → 1.0M.
func fmtCount(n int64) string {
	if n < 0 {
		return "-" + fmtCount(-n)
	}
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 100_000:
		return fmt.Sprintf("%dk", n/1_000)
	case n >= 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return commafy(n)
	}
}

// stringBuilder is a thin wrapper so we don't import strings.Builder
// across every CLI file. The package gets enough Markdown rendering to
// justify a tiny helper.
type stringBuilder struct {
	buf []byte
}

func (b *stringBuilder) Writef(format string, args ...any) {
	b.buf = append(b.buf, []byte(fmt.Sprintf(format, args...))...)
}

func (b *stringBuilder) String() string { return string(b.buf) }

// commafy adds thousands separators to a non-negative int64.
func commafy(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	// Walk back from the end, inserting commas every 3 digits.
	var b strings.Builder
	count := 0
	for i := len(s) - 1; i >= 0; i-- {
		if count > 0 && count%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
		count++
	}
	// Reverse.
	rs := []byte(b.String())
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	return string(rs)
}
