package mail

import (
	"context"
	"strings"
	"testing"
)

// TestRenderAllTemplates runs every shipped template through the renderer
// to catch syntax errors / missing files at CI time. The data map is the
// union of keys any template references; templates that don't reference a
// key simply ignore it.
func TestRenderAllTemplates(t *testing.T) {
	cfg := Config{
		AppName: "HAULA Fleet",
		AppURL:  "https://app.example.com",
		From:    "no-reply@example.com",
	}
	cases := []struct {
		template string
		mustHave []string // substrings that must appear in the rendered text body
	}{
		{
			template: "welcome",
			mustHave: []string{"Welcome", "alex", "Open HAULA Fleet", "https://app.example.com/login"},
		},
		{
			template: "verify_email",
			mustHave: []string{"Confirm", "Alex Kiyingi", "https://app.example.com/verify-email?token=abc", "24 hour"},
		},
		{
			template: "password_reset",
			mustHave: []string{"Reset", "alex", "https://app.example.com/reset-password?token=abc", "60 minutes"},
		},
		{
			template: "password_changed",
			mustHave: []string{"changed", "alex", "10.0.0.1", "/forgot-password"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.template, func(t *testing.T) {
			req := SendRequest{
				To:       "alex@example.com",
				Subject:  "test subject",
				Template: tc.template,
				Data: map[string]any{
					"Username":      "alex",
					"FullName":      "Alex Kiyingi",
					"VerifyURL":     "https://app.example.com/verify-email?token=abc",
					"ResetURL":      "https://app.example.com/reset-password?token=abc",
					"ExpiryHours":   24,
					"ExpiryMinutes": 60,
					"ChangedAt":     "Mon, 03 May 2026 10:00:00 UTC",
					"IP":            "10.0.0.1",
					"UserAgent":     "Mozilla/5.0",
				},
			}
			html, text, err := render(cfg, req)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if html == "" {
				t.Fatal("html body empty")
			}
			if text == "" {
				t.Fatal("text body empty")
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(text, want) && !strings.Contains(html, want) {
					t.Errorf("missing %q in either body", want)
				}
			}
		})
	}
}

// TestLogMailerRendersWithoutSMTP asserts the dev fallback path works end
// to end without a network. Any rendering error would surface as a Send error.
func TestLogMailerRendersWithoutSMTP(t *testing.T) {
	m := New(Config{
		// SMTP_HOST blank → New returns LogMailer
		AppName: "Test",
		AppURL:  "http://localhost:3000",
	})
	if _, ok := m.(*LogMailer); !ok {
		t.Fatalf("expected LogMailer when Host is empty; got %T", m)
	}
	err := m.Send(context.Background(), SendRequest{
		To:       "alex@example.com",
		Subject:  "hi",
		Template: "welcome",
		Data:     map[string]any{"Username": "alex", "FullName": "Alex"},
	})
	if err != nil {
		t.Fatalf("LogMailer.Send: %v", err)
	}
}

func TestSendRequiresTo(t *testing.T) {
	m := &LogMailer{Cfg: Config{AppName: "x"}}
	err := m.Send(context.Background(), SendRequest{Template: "welcome"})
	if err == nil {
		t.Fatal("expected error on empty To")
	}
}

func TestUnknownTemplate(t *testing.T) {
	_, _, err := render(Config{AppName: "x", AppURL: "y"}, SendRequest{Template: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}
