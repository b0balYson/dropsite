package forms

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http/httptest"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// smtpServer is a tiny SMTP server for tests: optional implicit TLS,
// AUTH PLAIN, and a configurable reply to RCPT.
type smtpServer struct {
	ln       net.Listener
	tls      *tls.Config
	rcptCode int

	mu   sync.Mutex
	auth string
	from string
	to   string
	data string
}

func startSMTP(t *testing.T, implicitTLS bool) (*smtpServer, *x509.CertPool) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	cert := ts.TLS.Certificates[0]
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	ts.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpServer{ln: ln, rcptCode: 250}
	if implicitTLS {
		s.tls = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	t.Cleanup(func() { _ = ln.Close() })
	go s.serve()
	return s, pool
}

func (s *smtpServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *smtpServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		if s.tls != nil {
			c = tls.Server(c, s.tls)
		}
		go s.session(c)
	}
}

func (s *smtpServer) session(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	reply := func(line string) { _, _ = io.WriteString(c, line+"\r\n") }
	reply("220 test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch cmd {
		case "EHLO":
			reply("250-test")
			reply("250 AUTH PLAIN")
		case "AUTH":
			s.mu.Lock()
			s.auth = line
			s.mu.Unlock()
			reply("235 ok")
		case "MAIL":
			s.mu.Lock()
			s.from = line
			s.mu.Unlock()
			reply("250 ok")
		case "RCPT":
			s.mu.Lock()
			s.to = line
			s.mu.Unlock()
			reply(strconv.Itoa(s.rcptCode) + " rcpt")
		case "DATA":
			reply("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.data = b.String()
			s.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 unknown")
		}
	}
}

func TestSendMailImplicitTLSWithAuth(t *testing.T) {
	srv, pool := startSMTP(t, true)
	cfg := SMTPConfig{
		Host: "127.0.0.1", Port: srv.port(), Username: "robot", Password: "s3cret-pass",
		From: "forms@example.com", Security: SMTPTLS, TLS: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
	body := "Name: Ann\nBcc: victim@example.com\nПривет"
	if err := SendMail(context.Background(), cfg, "owner@example.com", "Новая заявка: lead\r\nBcc: x@y.z", body); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	dec, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(srv.auth, "AUTH PLAIN "))
	if string(dec) != "\x00robot\x00s3cret-pass" {
		t.Fatalf("auth %q", dec)
	}
	if !strings.Contains(srv.from, "<forms@example.com>") || !strings.Contains(srv.to, "<owner@example.com>") {
		t.Fatalf("envelope %q %q", srv.from, srv.to)
	}
	msg, err := mail.ReadMessage(strings.NewReader(srv.data))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Bcc") != "" || len(msg.Header["Bcc"]) != 0 {
		t.Fatal("header injected")
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if subj != "Новая заявка: lead Bcc: x@y.z" {
		t.Fatalf("subject %q", subj)
	}
	text, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if !strings.Contains(string(text), "Привет") || !strings.Contains(string(text), "Bcc: victim@example.com") {
		t.Fatalf("body %q", text)
	}
}

func TestSendMailErrors(t *testing.T) {
	srv, _ := startSMTP(t, false)
	cfg := SMTPConfig{Host: "127.0.0.1", Port: srv.port(), From: "forms@example.com", Security: SMTPNone}
	if err := SendMail(context.Background(), cfg, "owner@example.com", "s", "b"); err != nil {
		t.Fatalf("plain relay: %v", err)
	}
	srv.rcptCode = 550
	err := SendMail(context.Background(), cfg, "owner@example.com", "s", "b")
	if err == nil || !IsPermanent(err) {
		t.Fatalf("550 must be permanent: %v", err)
	}
	srv.rcptCode = 451
	if err := SendMail(context.Background(), cfg, "owner@example.com", "s", "b"); err == nil || IsPermanent(err) {
		t.Fatalf("451 must be temporary: %v", err)
	}
	starttls := cfg
	starttls.Security = SMTPStartTLS
	if err := SendMail(context.Background(), starttls, "owner@example.com", "s", "b"); err == nil || !IsPermanent(err) {
		t.Fatalf("missing STARTTLS: %v", err)
	}
	// net/smtp refuses to send a password without TLS to a non-local host;
	// with a local host it is allowed, so only check the error never leaks it.
	withPass := cfg
	withPass.Username, withPass.Password = "u", "leak-me"
	srv.rcptCode = 550
	if err := SendMail(context.Background(), withPass, "owner@example.com", "s", "b"); err == nil || strings.Contains(err.Error(), "leak-me") {
		t.Fatalf("password leak: %v", err)
	}
	if err := SendMail(context.Background(), cfg, "bad\r\nBcc: x@y.z", "s", "b"); err == nil || !IsPermanent(err) {
		t.Fatal("bad recipient accepted")
	}
	if err := SendMail(context.Background(), SMTPConfig{}, "owner@example.com", "s", "b"); err == nil || !IsPermanent(err) {
		t.Fatal("unconfigured accepted")
	}
	down := cfg
	down.Port = 1
	if err := SendMail(context.Background(), down, "owner@example.com", "s", "b"); err == nil || IsPermanent(err) {
		t.Fatalf("connection refused must be temporary: %v", err)
	}
}

func TestParseEmail(t *testing.T) {
	for in, ok := range map[string]bool{
		"a@example.com": true, " a@example.com ": true, "Ann <a@example.com>": false, "a@localhost": false,
		"a@example.com\r\nBcc: b@c.d": false, "a@b.c,d@e.f": false, "": false,
	} {
		if _, got := ParseEmail(in); got != ok {
			t.Errorf("%q: %v", in, got)
		}
	}
}
