// SPDX-License-Identifier: Apache-2.0

package store

import (
	"net/url"
	"regexp"
)

// keywordPassword matches a password in a libpq keyword/value DSN
// ("host=... password=secret ...").
var keywordPassword = regexp.MustCompile(`(?i)(password=)[^\s]+`)

// RedactDSN returns a database URL safe to log: the password is masked. It
// handles both the URL form (postgres://user:pass@host/db) and the libpq
// keyword form (host=... password=...). A control plane must be able to log
// "which database" without logging the credential (P2.M4 log-secret hygiene).
func RedactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if u.User != nil {
			if _, hasPassword := u.User.Password(); hasPassword {
				u.User = url.UserPassword(u.User.Username(), "xxxxx")
			}
		}
		return u.String()
	}
	return keywordPassword.ReplaceAllString(dsn, "${1}xxxxx")
}
