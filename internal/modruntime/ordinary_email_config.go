package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

const ordinaryEmailQueue = "ordinary_smtp_v1"

type OrdinaryEmailTransport interface {
	SendOrdinary(context.Context, string, string, string, string, string, func() error) domain.OrdinarySMTPResult
}

type OrdinaryEmailConfig struct {
	Enabled                    bool
	From, Host, Port, Username string
	Password                   string `json:"-"`
}

// Global managed intents retain this exact configuration and fingerprint.
// The separate Inbox owner port never falls back to it or queries SYS credentials.
func LoadOrdinaryEmailConfig(getenv func(string) string) (OrdinaryEmailConfig, error) {
	c := OrdinaryEmailConfig{From: strings.TrimSpace(getenv("MAILER_SENDER_EMAIL")), Host: strings.TrimSpace(getenv("SMTP_ADDRESS")), Port: strings.TrimSpace(getenv("SMTP_PORT")), Username: getenv("SMTP_USERNAME"), Password: getenv("SMTP_PASSWORD")}
	if c.From == "" {
		c.From = strings.TrimSpace(getenv("SMTP_FROM_EMAIL"))
	}
	flag := strings.TrimSpace(getenv("ORDINARY_EMAIL_WORKER_ENABLED"))
	if flag != "" && flag != "true" && flag != "false" {
		return c, errors.New("ORDINARY_EMAIL_WORKER_ENABLED must be true or false")
	}
	c.Enabled = flag == "true"
	if c.Port == "" {
		c.Port = "587"
	}
	if c.Enabled && !c.valid() {
		return c, errors.New("ordinary email requires explicit global SMTP host, port and sender")
	}
	return c, nil
}
func (c OrdinaryEmailConfig) valid() bool {
	a, err := mail.ParseAddress(c.From)
	port, e := strconv.Atoi(c.Port)
	return err == nil && a.Address == c.From && !strings.ContainsAny(c.From, "\r\n") && c.Host != "" && !strings.ContainsAny(c.Host, "\r\n\x00") && e == nil && port > 0 && port <= 65535
}
func (c OrdinaryEmailConfig) key() string {
	// Password rotation does not change routing; hostname, username and sender do.
	raw, _ := json.Marshal([]string{c.Host, c.Port, c.Username, c.From})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
