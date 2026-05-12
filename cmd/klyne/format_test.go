package main

import "testing"

func TestShortID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"01234567", "01234567"},
		{"0123456789", "01234567"},
		{"feedfacebeef", "feedface"},
	}
	for _, c := range cases {
		if got := shortID(c.in); got != c.want {
			t.Errorf("shortID(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestShortPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/a", "/a"},
		{"a/b", "a/b"},
		{"/foo/bar/baz", ".../bar/baz"},
		{"/Users/me/code/proj", ".../code/proj"},
		{"trailing/slash/", "trailing/slash"},
		{"keep/last/two/segments/", ".../two/segments"},
	}
	for _, c := range cases {
		if got := shortPath(c.in); got != c.want {
			t.Errorf("shortPath(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestFmtCount(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1234, "1,234"},
		{12_345, "12.3k"},
		{99_999, "100.0k"},
		{100_000, "100k"},
		{1_500_000, "1.5M"},
		{-1234, "-1,234"},
	}
	for _, c := range cases {
		if got := fmtCount(c.in); got != c.want {
			t.Errorf("fmtCount(%d) = %q; want %q", c.in, got, c.want)
		}
	}
}
