package mail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strings"
)

// SMTPMailer dials the configured SMTP server and sends a multipart/alt
// message. Authentication is skipped when User is empty (useful for relays
// like MailHog that accept unauthenticated connections).
type SMTPMailer struct {
	Cfg Config
}

func (m *SMTPMailer) Send(ctx context.Context, req SendRequest) error {
	// Pre-flight cancellation check. Cheap and avoids dialing if the
	// caller already bailed (e.g. the originating HTTP request timed out
	// while we were waiting for our turn in a bounded worker pool).
	if err := ctx.Err(); err != nil {
		return err
	}
	if req.To == "" {
		return fmt.Errorf("mail: empty To")
	}
	html, text, err := render(m.Cfg, req)
	if err != nil {
		return err
	}
	body, err := buildMIME(m.Cfg.From, req.To, req.Subject, html, text)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", m.Cfg.Host, m.Cfg.Port)

	// ctx-bounded TCP dial. The dial is the slowest part of the SMTP
	// handshake and the most likely source of caller-cancelled latency,
	// so this is where ctx earns its keep. Once we have an open socket
	// we fall back to net/smtp's well-tested handshake — reimplementing
	// EHLO/STARTTLS/AUTH/MAIL/RCPT/DATA/QUIT to thread ctx through every
	// step would be more honest but risks regressions in a battle-tested
	// path; the typical post-dial exchange completes in tens of ms.
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	client, err := smtp.NewClient(conn, m.Cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if m.Cfg.User != "" {
		auth := smtp.PlainAuth("", m.Cfg.User, m.Cfg.Pass, m.Cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(m.Cfg.From); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(req.To); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp body write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp body close: %w", err)
	}
	return client.Quit()
}

// buildMIME assembles a minimal RFC-2046 multipart/alternative message with
// both text and html parts. Using 7bit + UTF-8 + quoted-printable would be
// more correct for non-ASCII bodies; we keep it simple for now. Most
// consumer mail clients accept this output with both parts visible.
func buildMIME(from, to, subject, html, text string) ([]byte, error) {
	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")

	// text part
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(text)
	b.WriteString("\r\n")

	// html part
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(html)
	b.WriteString("\r\n")

	// closing boundary
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

func randomBoundary() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "haula-" + hex.EncodeToString(buf), nil
}
