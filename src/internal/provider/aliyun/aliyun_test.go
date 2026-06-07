package aliyun

import "testing"

func TestNameHelpers(t *testing.T) {
	if got := fullName("example.com", "home"); got != "home.example.com" {
		t.Fatalf("fullName = %q", got)
	}
	if got := fullName("example.com", "example.com"); got != "example.com" {
		t.Fatalf("fullName root = %q", got)
	}
	if got := rrFromName("example.com", "home.example.com"); got != "home" {
		t.Fatalf("rrFromName = %q", got)
	}
	if got := rrFromName("example.com", "example.com"); got != "@" {
		t.Fatalf("rrFromName root = %q", got)
	}
}

func TestCanonicalQuerySortsAndEncodes(t *testing.T) {
	got := canonicalQuery(map[string]string{
		"b": "two words",
		"a": "~",
	})
	want := "a=~&b=two%20words"
	if got != want {
		t.Fatalf("canonicalQuery = %q, want %q", got, want)
	}
}
