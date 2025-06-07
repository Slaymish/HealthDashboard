package main

import (
	"database/sql"
	"testing"
)

func TestFormatNote(t *testing.T) {
	cases := []struct {
		note     sql.NullString
		expected string
	}{
		{sql.NullString{Valid: false}, "–"},
		{sql.NullString{String: "", Valid: true}, "–"},
		{sql.NullString{String: "{Hello true}", Valid: true}, "Hello"},
		{sql.NullString{String: "{Sample false}", Valid: true}, "Sample"},
		{sql.NullString{String: "Plain note", Valid: true}, "Plain note"},
	}

	for _, c := range cases {
		got := FormatNote(c.note)
		if got != c.expected {
			t.Errorf("FormatNote(%v) = %q, want %q", c.note, got, c.expected)
		}
	}
}

func TestFmtF2(t *testing.T) {
	if got := fmtF2(nil); got != "–" {
		t.Errorf("fmtF2(nil) = %q, want '–'", got)
	}
	v := 1.234
	if got := fmtF2(&v); got != "1.2" {
		t.Errorf("fmtF2(1.234) = %q, want '1.2'", got)
	}
}

func TestFmtInt(t *testing.T) {
	if got := fmtInt(nil); got != "–" {
		t.Errorf("fmtInt(nil) = %q, want '–'", got)
	}
	v := 7
	if got := fmtInt(&v); got != "7" {
		t.Errorf("fmtInt(7) = %q, want '7'", got)
	}
}

func TestFmtIntWithSign(t *testing.T) {
	if got := fmtIntWithSign(nil); got != "–" {
		t.Errorf("fmtIntWithSign(nil) = %q, want '–'", got)
	}
	a := 5
	if got := fmtIntWithSign(&a); got != "+5" {
		t.Errorf("fmtIntWithSign(5) = %q, want '+5'", got)
	}
	b := -3
	if got := fmtIntWithSign(&b); got != "-3" {
		t.Errorf("fmtIntWithSign(-3) = %q, want '-3'", got)
	}
}

func TestOr(t *testing.T) {
	if got := or(nil, 4); got != 4 {
		t.Errorf("or(nil,4) = %d, want 4", got)
	}
	v := 9
	if got := or(&v, 4); got != 9 {
		t.Errorf("or(&9,4) = %d, want 9", got)
	}
}

func TestMod(t *testing.T) {
	if got := mod(10, 3); got != 1 {
		t.Errorf("mod(10,3) = %d, want 1", got)
	}
}

func TestSub(t *testing.T) {
	if got := sub(7, 2); got != 5 {
		t.Errorf("sub(7,2) = %d, want 5", got)
	}
}
