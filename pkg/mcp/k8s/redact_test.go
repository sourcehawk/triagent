package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactConfigMap_Heuristics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"password key", "admin_password", "hunter2", redactedValue},
		{"uppercase SECRET key", "DB_SECRET", "anything", redactedValue},
		{"api-key hyphen form", "api-key", "abc123", redactedValue},
		{"api_key underscore form", "api_key", "abc123", redactedValue},
		{"token key", "auth_token", "short", redactedValue},
		{"bearer in key", "bearer_header", "whatever", redactedValue},

		{"jwt value", "id_token", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc", redactedValue},
		{"long base64-ish value", "blob", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", redactedValue},
		{"pem private key", "cert", "-----BEGIN PRIVATE KEY-----\nMIIE..\n-----END PRIVATE KEY-----", redactedValue},

		{"plain config string passes through", "log_level", "debug", "debug"},
		{"url passes through", "api_url", "https://example.com/api", "https://example.com/api"},
		{"multi-line yaml passes through", "config_yaml", "foo: bar\nbaz: qux", "foo: bar\nbaz: qux"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RedactConfigMap(map[string]string{tc.key: tc.value})
			assert.Equal(t, tc.want, got[tc.key], "%q=%q", tc.key, tc.value)
		})
	}
}

func TestRedactConfigMap_LeavesBenignKeysAlone(t *testing.T) {
	t.Parallel()
	in := map[string]string{
		"log_level":   "debug",
		"pool_size":   "10",
		"greeting":    "hello world",
		"tenant_name": "acme",
	}
	out := RedactConfigMap(in)
	for k, v := range in {
		assert.Equal(t, v, out[k], "%q changed", k)
	}
}

func TestRedactSecret_AlwaysRedactsAllValues(t *testing.T) {
	t.Parallel()

	in := map[string][]byte{
		"username": []byte("alice"),
		"password": []byte("any"),
		"token":    []byte("anything"),
	}
	out := RedactSecret(in)
	assert.Len(t, out, len(in))
	for k, v := range out {
		assert.Equal(t, redactedValue, v, "%q not redacted", k)
	}
}
