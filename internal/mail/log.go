package mail

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// LogMailer is the dev-mode fallback used when SMTP_HOST is unset. It
// renders the email and writes it to the standard logger so password-reset
// and verification links remain visible without a real mail server.
type LogMailer struct {
	Cfg Config
}

func (m *LogMailer) Send(ctx context.Context, req SendRequest) error {
	// Honour cancellation before we do any work — useful for tests and
	// for the rare case a caller bails after queuing the send.
	if err := ctx.Err(); err != nil {
		return err
	}
	if req.To == "" {
		return fmt.Errorf("mail: empty To")
	}
	_, text, err := render(m.Cfg, req)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("\n────── EMAIL ────────────────────────────────────────────────────\n")
	fmt.Fprintf(&b, "From:     %s\n", m.Cfg.From)
	fmt.Fprintf(&b, "To:       %s\n", req.To)
	fmt.Fprintf(&b, "Subject:  %s\n", req.Subject)
	fmt.Fprintf(&b, "Template: %s\n", req.Template)
	b.WriteString("─────────────────────────────────────────────────────────────────\n")
	b.WriteString(text)
	b.WriteString("\n─────────────────────────────────────────────────────────────────\n")
	log.Print(b.String())
	return nil
}
