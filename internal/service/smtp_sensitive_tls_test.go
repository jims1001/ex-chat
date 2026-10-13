package service

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const sensitiveSMTPTestChildEnv = "EXCHAT_SENSITIVE_SMTP_TLS_TEST_CHILD"

// Use a fresh process so crypto/x509 loads the temporary CA before any other
// test can populate its system-root cache. Only the child process trusts this
// CA; neither the host trust store nor the parent environment is changed.
func TestSensitiveSMTPVerifiedTLS(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "aix":
	default:
		t.Skip("isolated SSL_CERT_FILE trust is supported on Unix certificate-file platforms")
	}
	if dir := os.Getenv(sensitiveSMTPTestChildEnv); dir != "" {
		runSensitiveSMTPVerifiedTLS(t, dir)
		return
	}

	ca, key := newSensitiveSMTPTestCA(t)
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0600))
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ca-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600))
	certDir := filepath.Join(dir, "empty-cert-dir")
	require.NoError(t, os.Mkdir(certDir, 0700))
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSensitiveSMTPVerifiedTLS$", "-test.count=1", "-test.timeout=30s", "-test.v")
	cmd.Env = append(os.Environ(), sensitiveSMTPTestChildEnv+"="+dir, "SSL_CERT_FILE="+caFile, "SSL_CERT_DIR="+certDir)
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "isolated verified-TLS SMTP tests failed:\n%s", output)
	t.Logf("isolated verified-TLS SMTP tests:\n%s", output)
}

func runSensitiveSMTPVerifiedTLS(t *testing.T, dir string) {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	require.NoError(t, err)
	caBlock, _ := pem.Decode(caPEM)
	require.NotNil(t, caBlock)
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err)
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	require.NoError(t, err)
	keyBlock, _ := pem.Decode(keyPEM)
	require.NotNil(t, keyBlock)
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
	trustedKey, ok := key.(ed25519.PrivateKey)
	require.True(t, ok)
	untrustedCA, untrustedKey := newSensitiveSMTPTestCA(t)

	t.Setenv("FRONTEND_URL", "https://example.test")
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	for _, certificate := range []struct {
		name string
		ca   *x509.Certificate
		key  ed25519.PrivateKey
		ip   net.IP
	}{
		{"trusted", ca, trustedKey, net.ParseIP("127.0.0.1")},
		{"untrusted", untrustedCA, untrustedKey, net.ParseIP("127.0.0.1")},
		{"hostname-mismatch", ca, trustedKey, net.ParseIP("127.0.0.2")},
	} {
		cert := newSensitiveSMTPTestCertificate(t, certificate.ca, certificate.key, certificate.ip)
		for _, flow := range []string{"password-reset-starttls", "agent-invitation-starttls", "implicit-tls"} {
			t.Run(certificate.name+"/"+flow, func(t *testing.T) {
				implicitTLS := flow == "implicit-tls"
				sender, fixture := newSensitiveSMTPTLSFixture(t, cert, implicitTLS)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				service := &EmailService{fromEmail: "security@example.test"}
				service.SetSender(sender)
				token := strings.Repeat("c", 64) // Disposable credential accepted by both email entry points.
				var state string
				var err error
				switch flow {
				case "password-reset-starttls":
					err = service.SendPasswordResetEmail(ctx, "user@example.test", token, time.Now().Add(time.Hour))
				case "agent-invitation-starttls":
					state, err = service.SendAgentInvitationEmail(ctx, "user@example.test", token, 42, time.Now().Add(time.Hour))
				case "implicit-tls":
					err = sender.sendSensitiveSMTP(ctx, "security@example.test", "user@example.test", []byte("Subject: Local TLS test\r\n\r\n"+token+"\r\n"), true)
				}
				fixture.wait(t)
				require.True(t, fixture.tlsAttempted)
				for _, command := range fixture.commands {
					if !command.encrypted {
						require.Contains(t, []string{"EHLO localhost", "STARTTLS"}, command.line, "only TLS negotiation may use plaintext")
					}
				}
				if certificate.name != "trusted" {
					require.Error(t, err)
					require.NotContains(t, err.Error(), token)
					require.False(t, fixture.tlsEstablished)
					require.Error(t, fixture.err, "the server must observe a failed TLS handshake")
					require.Empty(t, fixture.body)
					for _, command := range fixture.commands {
						require.Contains(t, []string{"EHLO localhost", "STARTTLS"}, command.line, "failed TLS must stop before AUTH, MAIL, RCPT, or DATA")
					}
					if flow == "agent-invitation-starttls" {
						require.Equal(t, "delivery_unknown", state)
					}
					return
				}
				require.NoError(t, err)
				require.NoError(t, fixture.err)
				require.True(t, fixture.tlsEstablished)
				require.Contains(t, fixture.body, token)
				require.Contains(t, fixture.commands, sensitiveSMTPTLSCommand{"MAIL FROM:<security@example.test>", true})
				require.Contains(t, fixture.commands, sensitiveSMTPTLSCommand{"RCPT TO:<user@example.test>", true})
				require.Equal(t, sensitiveSMTPTLSCommand{"QUIT", true}, fixture.commands[len(fixture.commands)-1])
				require.True(t, fixture.authenticated, "disposable SMTP credentials must be sent only through verified TLS")
				if flow == "agent-invitation-starttls" {
					require.Equal(t, "submitted", state)
				}
			})
		}
	}
}

func newSensitiveSMTPTestCA(t *testing.T) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ExChat disposable SMTP test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return ca, key
}

func newSensitiveSMTPTestCertificate(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, ip net.IP) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{ip},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, pub, caKey)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: key}
}

type sensitiveSMTPTLSCommand struct {
	line      string
	encrypted bool
}

// Each fixture owns one loopback connection. Results are inspected only after
// done closes, and cleanup closes and joins the server even when an assertion
// aborts a test. Both accept and protocol I/O have independent safety deadlines.
type sensitiveSMTPTLSFixture struct {
	mu             sync.Mutex
	conn           net.Conn
	closed         bool
	done           chan struct{}
	commands       []sensitiveSMTPTLSCommand
	body           string
	tlsAttempted   bool
	tlsEstablished bool
	authenticated  bool
	err            error
}

func newSensitiveSMTPTLSFixture(t *testing.T, cert tls.Certificate, implicitTLS bool) (*SMTPEmailSender, *sensitiveSMTPTLSFixture) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	require.NoError(t, listener.SetDeadline(time.Now().Add(10*time.Second)))
	f := &sensitiveSMTPTLSFixture{done: make(chan struct{})}
	t.Cleanup(func() {
		_ = listener.Close()
		f.mu.Lock()
		f.closed = true
		if f.conn != nil {
			_ = f.conn.Close()
		}
		f.mu.Unlock()
		f.wait(t)
	})
	go func() {
		defer close(f.done)
		conn, err := listener.Accept()
		if err != nil {
			f.err = err
			return
		}
		defer conn.Close()
		f.mu.Lock()
		f.conn = conn
		closed := f.closed
		f.mu.Unlock()
		if closed {
			return
		}
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			f.err = err
			return
		}
		f.err = f.serve(conn, cert, implicitTLS)
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return NewSMTPEmailSender(host, port, "disposable-test-user", "disposable-test-password"), f
}

func (f *sensitiveSMTPTLSFixture) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("loopback TLS SMTP fixture did not stop")
	}
}

func (f *sensitiveSMTPTLSFixture) serve(conn net.Conn, cert tls.Certificate, implicitTLS bool) error {
	startTLS := func() error {
		f.tlsAttempted = true
		tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		if err := tlsConn.Handshake(); err != nil {
			return err
		}
		conn = tlsConn
		f.tlsEstablished = true
		return nil
	}
	if implicitTLS {
		if err := startTLS(); err != nil {
			return err
		}
	}
	write := func(response string) error { _, err := io.WriteString(conn, response+"\r\n"); return err }
	if err := write("220 loopback SMTP ready"); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		f.commands = append(f.commands, sensitiveSMTPTLSCommand{line, f.tlsEstablished})
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return errors.New("empty loopback SMTP command")
		}
		switch fields[0] {
		case "EHLO":
			if f.tlsEstablished {
				err = write("250-loopback\r\n250 AUTH PLAIN")
			} else {
				err = write("250-loopback\r\n250 STARTTLS")
			}
		case "STARTTLS":
			if f.tlsEstablished {
				return errors.New("unexpected repeated STARTTLS")
			}
			if err := write("220 begin TLS"); err != nil {
				return err
			}
			if err := startTLS(); err != nil {
				return err
			}
			reader = bufio.NewReader(conn)
		case "AUTH":
			if !f.tlsEstablished || len(fields) != 3 || fields[1] != "PLAIN" {
				return errors.New("AUTH must use PLAIN over TLS")
			}
			credentials, decodeErr := base64.StdEncoding.DecodeString(fields[2])
			if decodeErr != nil || string(credentials) != "\x00disposable-test-user\x00disposable-test-password" {
				return errors.New("incorrect disposable SMTP credentials")
			}
			f.authenticated = true
			err = write("235 authenticated")
		case "MAIL", "RCPT":
			if !f.tlsEstablished || !f.authenticated {
				return errors.New("envelope must follow TLS and AUTH")
			}
			err = write("250 accepted")
		case "DATA":
			if !f.tlsEstablished || !f.authenticated {
				return errors.New("DATA must follow TLS and AUTH")
			}
			if err := write("354 send message"); err != nil {
				return err
			}
			var body strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				if line == ".\r\n" {
					break
				}
				body.WriteString(line)
			}
			f.body = body.String()
			err = write("250 queued")
		case "QUIT":
			return write("221 goodbye")
		default:
			return fmt.Errorf("unexpected loopback SMTP command: %s", fields[0])
		}
		if err != nil {
			return err
		}
	}
}
