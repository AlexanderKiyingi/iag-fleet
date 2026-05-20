package config

import "testing"

func TestListenAddr(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("ADDR", "")
	t.Setenv("HTTP_PORT", "")

	if got := ListenAddr(); got != ":4008" {
		t.Fatalf("default = %q, want :4008", got)
	}

	t.Setenv("PORT", "8080")
	if got := ListenAddr(); got != ":8080" {
		t.Fatalf("PORT = %q, want :8080", got)
	}

	t.Setenv("PORT", "")
	t.Setenv("ADDR", ":4008")
	if got := ListenAddr(); got != ":4008" {
		t.Fatalf("ADDR = %q, want :4008", got)
	}

	t.Setenv("PORT", "3000")
	t.Setenv("ADDR", ":4008")
	if got := ListenAddr(); got != ":3000" {
		t.Fatalf("PORT overrides ADDR: got %q, want :3000", got)
	}
}
