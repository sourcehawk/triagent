package k8s

import (
	"regexp"
	"strings"
)

const redactedValue = "<redacted>"

// suspiciousKey matches ConfigMap keys that usually carry secrets.
var suspiciousKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|apikey|api[_-]?key|privatekey|private[_-]?key|bearer|session)`)

// base64ish matches long base64-ish strings. Used as one signal (not the only
// one) that a value is likely a credential rather than a plain config string.
var base64ish = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{40,}$`)

// jwt matches the "aaa.bbb.ccc" shape of a JWT, base64-url segments.
var jwt = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

// pemHeader matches any PEM-encoded block header.
var pemHeader = regexp.MustCompile(`-----BEGIN [A-Z ]+-----`)

// RedactConfigMap returns a copy of data where values that look like
// credentials are replaced with a placeholder. The decision is made
// per-entry, using both the key and value as signals.
func RedactConfigMap(data map[string]string) map[string]string {
	out := make(map[string]string, len(data))
	for k, v := range data {
		if shouldRedact(k, v) {
			out[k] = redactedValue
			continue
		}
		out[k] = v
	}
	return out
}

// RedactSecret returns a map with the same keys but every value replaced
// by the placeholder. Secret values are never served regardless of shape.
func RedactSecret(data map[string][]byte) map[string]string {
	out := make(map[string]string, len(data))
	for k := range data {
		out[k] = redactedValue
	}
	return out
}

func shouldRedact(key, value string) bool {
	if suspiciousKey.MatchString(key) {
		return true
	}
	if pemHeader.MatchString(value) {
		return true
	}
	// Single-line JWT-shaped value.
	if !strings.ContainsAny(value, " \n\r\t") && jwt.MatchString(value) {
		return true
	}
	// Long base64ish value with no spaces — likely a token or hash.
	if !strings.ContainsAny(value, " \n\r\t") && base64ish.MatchString(value) {
		return true
	}
	return false
}
