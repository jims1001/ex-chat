package service

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const ordinarySMTPTestChildEnv = "EXCHAT_ORDINARY_SMTP_TLS_TEST_CHILD"

// As with the credential-mail TLS tests, trust a disposable CA only inside an
// isolated child process, so production trust and the parent test process stay
// unchanged. All SMTP listeners remain ephemeral loopback fixtures.
func TestOrdinarySMTPVerifiedTLS(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "aix":
	default:
		t.Skip("isolated SSL_CERT_FILE trust requires a Unix certificate-file platform")
	}
	if dir := os.Getenv(ordinarySMTPTestChildEnv); dir != "" {
		runOrdinarySMTPVerifiedTLS(t, dir)
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
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestOrdinarySMTPVerifiedTLS$", "-test.count=1", "-test.timeout=30s", "-test.v")
	cmd.Env = append(os.Environ(), ordinarySMTPTestChildEnv+"="+dir, "SSL_CERT_FILE="+caFile, "SSL_CERT_DIR="+certDir)
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "isolated ordinary verified-TLS tests failed:\n%s", output)
	t.Logf("isolated ordinary verified-TLS tests:\n%s", output)
}

func runOrdinarySMTPVerifiedTLS(t *testing.T, dir string) {
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
		for _, mode := range []string{"starttls", "implicit"} {
			t.Run(certificate.name+"/"+mode, func(t *testing.T) {
				cert := newSensitiveSMTPTestCertificate(t, certificate.ca, certificate.key, certificate.ip)
				implicitTLS := mode == "implicit"
				sender, fixture := newSensitiveSMTPTLSFixture(t, cert, implicitTLS)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				hookCalls := 0
				hook := func() error { hookCalls++; return nil }
				var result OrdinarySMTPResult
				if implicitTLS {
					result = sender.sendOrdinarySMTP(ctx, "from@example.test", "to@example.test", []byte("Subject: Local TLS test\r\n\r\nordinary test body\r\n"), true, hook)
				} else {
					result = sender.SendOrdinary(ctx, "from@example.test", "to@example.test", "TLS local test", "ordinary test body", "ordinary test body", hook)
				}
				fixture.wait(t)
				require.True(t, fixture.tlsAttempted)
				for _, command := range fixture.commands {
					if !command.encrypted {
						require.Contains(t, []string{"EHLO localhost", "STARTTLS"}, command.line)
					}
				}
				if certificate.name != "trusted" {
					require.Equal(t, OrdinarySMTPPermanentFailure, result.Outcome)
					require.Zero(t, hookCalls)
					require.False(t, fixture.tlsEstablished)
					require.False(t, fixture.authenticated)
					require.Empty(t, fixture.body)
					for _, command := range fixture.commands {
						require.Contains(t, []string{"EHLO localhost", "STARTTLS"}, command.line)
					}
					return
				}
				require.Equal(t, OrdinarySMTPAccepted, result.Outcome)
				require.Equal(t, 1, hookCalls)
				require.NoError(t, fixture.err)
				require.True(t, fixture.tlsEstablished)
				require.True(t, fixture.authenticated)
				require.Contains(t, fixture.body, "ordinary test body")
				require.Contains(t, fixture.commands, sensitiveSMTPTLSCommand{"MAIL FROM:<from@example.test>", true})
				require.Contains(t, fixture.commands, sensitiveSMTPTLSCommand{"RCPT TO:<to@example.test>", true})
			})
		}
	}
}
