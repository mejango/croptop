package main

import "testing"

func TestParseShortcut(t *testing.T) {
	for spec, want := range map[string]string{"cmd+ctrl+shift+c": "@^$C", "cmd+shift+c": "@$C", "Ctrl+Opt+7": "^~7"} {
		got, _, err := parseShortcut(spec)
		if err != nil || got != want {
			t.Fatalf("%s: got %q %v want %q", spec, got, err, want)
		}
	}
	if _, _, err := parseShortcut("shift+c"); err == nil {
		t.Fatal("shift alone should be rejected")
	}
}
