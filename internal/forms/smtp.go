package forms

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// SMTP security modes.
const (
	SMTPStartTLS = "starttls"
	SMTPTLS      = "tls"
	SMTPNone     = "none"
)

// SMTPConfig is the installation's outgoing mail server.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	Security string
	// TLS overrides the TLS configuration (tests); ServerName is set to Host.
	TLS *tls.Config
}

// Configured reports whether mail can be sent at all.
func (c SMTPConfig) Configured() bool { return c.Host != "" && c.Port > 0 && c.From != "" }

// ValidSecurity reports whether s is a known security mode.
func ValidSecurity(s string) bool { return s == SMTPStartTLS || s == SMTPTLS || s == SMTPNone }

// ParseEmail validates a single plain address and returns it without a
// display name. Line breaks and other header tricks are rejected.
func ParseEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "\r\n<>,;\"") {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s || !strings.Contains(s[strings.LastIndexByte(s, '@')+1:], ".") {
		return "", false
	}
	return a.Address, true
}

func (c SMTPConfig) tlsConfig() *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.TLS != nil {
		cfg = c.TLS.Clone()
	}
	cfg.ServerName = c.Host
	return cfg
}

const smtpTimeout = 30 * time.Second

// SendMail delivers one plain-text message. The password never appears in
// returned errors.
func SendMail(ctx context.Context, c SMTPConfig, to, subject, body string) error {
	if !c.Configured() || !ValidSecurity(c.Security) {
		return Permanent("e-mail is not configured")
	}
	if _, ok := ParseEmail(to); !ok {
		return Permanent("invalid recipient address")
	}
	from, ok := ParseEmail(c.From)
	if !ok {
		return Permanent("invalid sender address")
	}
	msg, err := buildMessage(from, to, subject, body)
	if err != nil {
		return Permanent("could not build the message")
	}
	cl, err := dialSMTP(ctx, c)
	if err != nil {
		return err
	}
	defer cl.Close()
	if err := converse(cl, c, from, to, msg); err != nil {
		return smtpError(err)
	}
	return nil
}

func dialSMTP(ctx context.Context, c SMTPConfig) (*smtp.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	d := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if c.Security == SMTPTLS {
		td := &tls.Dialer{NetDialer: d, Config: c.tlsConfig()}
		conn, err = td.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, withDetail(false, errSMTPUnreachable, "smtp: connection failed: "+err.Error())
	}
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		_ = conn.Close()
		return nil, smtpError(err)
	}
	return cl, nil
}

func converse(cl *smtp.Client, c SMTPConfig, from, to string, msg []byte) error {
	if err := cl.Hello(heloName(from)); err != nil {
		return err
	}
	if c.Security == SMTPStartTLS {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return withDetail(true, errSMTPRejected, "smtp: the server does not offer STARTTLS")
		}
		if err := cl.StartTLS(c.tlsConfig()); err != nil {
			return err
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return err
		}
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

func heloName(from string) string {
	return from[strings.LastIndexByte(from, '@')+1:]
}

// Public messages of SMTP failures: what the mail server said stays in the
// server log (SendError.Detail).
const (
	errSMTPUnreachable = "e-mail: the mail server of this installation is unreachable"
	errSMTPRejected    = "e-mail: the mail server of this installation refused the message"
	errSMTPTemporary   = "e-mail: the mail server of this installation is temporarily refusing messages"
	errSMTPFailed      = "e-mail: sending failed"
)

// smtpError classifies a protocol error: 5xx replies are permanent.
func smtpError(err error) error {
	var se *SendError
	if errors.As(err, &se) {
		return se
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		detail := fmt.Sprintf("smtp: %d %s", te.Code, te.Msg)
		if te.Code >= 500 {
			return withDetail(true, errSMTPRejected, detail)
		}
		return withDetail(false, errSMTPTemporary, detail)
	}
	return withDetail(false, errSMTPFailed, "smtp: "+err.Error())
}

func buildMessage(from, to, subject, body string) ([]byte, error) {
	var b bytes.Buffer
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	subject = cleanText(subject, false)
	headers := [][2]string{
		{"From", from},
		{"To", to},
		{"Subject", mime.QEncoding.Encode("utf-8", subject)},
		{"Date", time.Now().UTC().Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + heloName(from) + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=UTF-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
		{"Auto-Submitted", "auto-generated"},
	}
	for _, h := range headers {
		if strings.ContainsAny(h[1], "\r\n") {
			return nil, errors.New("line break in header")
		}
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
