package security

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateSafeURL(t *testing.T) {
	// Blocked URLs
	blocked := []string{
		"http://127.0.0.1/admin",
		"http://127.0.0.2:8080/test",
		"http://localhost:3000",
		"http://sub.localhost/api",
		"http://169.254.169.254/latest/meta-data",
		"http://10.0.0.1/internal",
		"http://172.16.0.1/",
		"http://192.168.1.1/router",
		"http://0.0.0.0:80",
		"ftp://example.com/file",
		"file:///etc/passwd",
		"gopher://example.com",
	}

	for _, u := range blocked {
		err := ValidateSafeURL(u)
		assert.Error(t, err, "URL should be blocked: %s", u)
	}

	// Reserved IP check
	assert.True(t, IsPrivateOrReservedIP(net.ParseIP("127.0.0.1")))
	assert.True(t, IsPrivateOrReservedIP(net.ParseIP("169.254.169.254")))
	assert.True(t, IsPrivateOrReservedIP(net.ParseIP("10.10.10.10")))
	assert.True(t, IsPrivateOrReservedIP(net.ParseIP("172.20.0.1")))
	assert.True(t, IsPrivateOrReservedIP(net.ParseIP("192.168.0.1")))
	assert.True(t, IsPrivateOrReservedIP(net.ParseIP("::1")))
	assert.False(t, IsPrivateOrReservedIP(net.ParseIP("8.8.8.8")))
	assert.False(t, IsPrivateOrReservedIP(net.ParseIP("1.1.1.1")))
}
