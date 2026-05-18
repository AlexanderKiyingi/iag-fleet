// Package mail sends transactional email (welcome, verify, reset, changed).
//
// Templates are rendered from embedded files; bodies are sent multipart/alt
// with both text/plain and text/html parts. Two implementations exist:
//
//   - SMTPMailer: dials the configured SMTP host using net/smtp.
//   - LogMailer:  writes the rendered email to stdout. Used in dev when
//     SMTP_HOST is unset, so password-reset and verify links are visible.
package mail

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	htmltmpl "html/template"
	"log/slog"
	"strings"
	texttmpl "text/template"
)

//go:embed templates/*.html templates/*.txt
var templatesFS embed.FS

// Config bundles SMTP credentials and the branding/link data that every
// template embeds. From, AppName, and AppURL are required by the templates;
// SMTP fields are optional — when Host is empty, the LogMailer is used.
type Config struct {
	Host    string
	Port    int
	User    string
	Pass    string
	From    string // e.g. "HAULA Fleet <noreply@haula.example>"
	AppName string // displayed in subjects and template headings
	AppURL  string // base URL the frontend serves; verification/reset links use this
}

// Mailer is the boundary between handlers and the email transport.
// Implementations must be safe to call from concurrent goroutines.
type Mailer interface {
	Send(ctx context.Context, req SendRequest) error
}

// SendRequest is what handlers hand to the mailer. Template names must
// match a file in templates/ without the extension; the mailer will look
// up both <name>.html and <name>.txt and assemble a multipart message.
type SendRequest struct {
	To       string
	Subject  string
	Template string
	Data     map[string]any
}

// New picks the right implementation for the supplied config. Falls back
// to LogMailer (and logs a warning) when SMTP_HOST is empty so that
// running the API without an SMTP server still yields visible verify and
// reset links during development.
func New(cfg Config) Mailer {
	if cfg.AppName == "" {
		cfg.AppName = "HAULA Fleet"
	}
	if cfg.AppURL == "" {
		cfg.AppURL = "http://localhost:3000"
	}
	if cfg.From == "" {
		cfg.From = "no-reply@localhost"
	}
	if cfg.Host == "" {
		slog.Warn("SMTP_HOST unset; using LogMailer — emails will print to stdout")
		return &LogMailer{Cfg: cfg}
	}
	return &SMTPMailer{Cfg: cfg}
}

// render reads the html and txt template files for the given name,
// executes them against data merged with branding fields, and returns
// both rendered bodies.
func render(cfg Config, req SendRequest) (htmlBody, textBody string, err error) {
	data := map[string]any{
		"AppName": cfg.AppName,
		"AppURL":  strings.TrimRight(cfg.AppURL, "/"),
		"Subject": req.Subject,
	}
	for k, v := range req.Data {
		data[k] = v
	}

	htmlBytes, err := templatesFS.ReadFile("templates/" + req.Template + ".html")
	if err != nil {
		return "", "", fmt.Errorf("read html template %q: %w", req.Template, err)
	}
	htmlT, err := htmltmpl.New("html").Parse(string(htmlBytes))
	if err != nil {
		return "", "", fmt.Errorf("parse html template %q: %w", req.Template, err)
	}
	var hb bytes.Buffer
	if err := htmlT.Execute(&hb, data); err != nil {
		return "", "", fmt.Errorf("exec html template %q: %w", req.Template, err)
	}

	textBytes, err := templatesFS.ReadFile("templates/" + req.Template + ".txt")
	if err != nil {
		return "", "", fmt.Errorf("read text template %q: %w", req.Template, err)
	}
	textT, err := texttmpl.New("text").Parse(string(textBytes))
	if err != nil {
		return "", "", fmt.Errorf("parse text template %q: %w", req.Template, err)
	}
	var tb bytes.Buffer
	if err := textT.Execute(&tb, data); err != nil {
		return "", "", fmt.Errorf("exec text template %q: %w", req.Template, err)
	}

	return hb.String(), tb.String(), nil
}
