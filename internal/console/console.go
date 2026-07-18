// SPDX-License-Identifier: Apache-2.0

// Package console embeds the read-only web console (phase1-build-plan.md
// M11): a static, dependency-free SPA served from the single rabi binary.
// It consumes the public REST API with the viewer's own bearer token —
// zero server-side session state, zero write endpoints (the page never
// issues anything but GET, proxy-asserted in the Playwright suite).
package console

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// Handler serves the console under /console/.
func Handler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("console: embedded tree missing: " + err.Error()) // impossible: compile-time embed
	}
	return http.StripPrefix("/console/", http.FileServer(http.FS(sub)))
}
