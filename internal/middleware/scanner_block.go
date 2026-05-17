package middleware

import (
	"net/http"
	"strings"
)

// _scannerPrefixes is the list of well-known path prefixes probed by automated
// vulnerability scanners and exploit tools. Requests for these paths are
// silently rejected with 404 so they never reach upstream services or generate
// noise in application logs. All legitimate gateway paths begin with /api/ or
// /health, so there is no risk of false positives.
var _scannerPrefixes = []string{
	"/.env",
	"/.git",
	"/.svn",
	"/.htaccess",
	"/.htpasswd",
	"/.DS_Store",
	"/wp-admin",
	"/wp-login.php",
	"/wp-content",
	"/wp-includes",
	"/xmlrpc.php",
	"/phpmyadmin",
	"/adminer",
	"/phpinfo.php",
	"/info.php",
	"/config.php",
	"/setup.php",
	"/install.php",
	"/shell.php",
	"/cmd.php",
	"/manager/html",
	"/console",
	"/actuator",
	"/solr",
	"/.well-known/security.txt",
}

// BlockScannerPaths returns a 404 for any request whose path matches a known
// automated scanner probe pattern. The match is case-insensitive prefix match.
func BlockScannerPaths(next http.Handler) http.Handler {
	// Lowercase the prefixes once at construction time.
	lc := make([]string, len(_scannerPrefixes))
	for i, p := range _scannerPrefixes {
		lc[i] = strings.ToLower(p)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.ToLower(r.URL.Path)
		for _, p := range lc {
			if path == p || strings.HasPrefix(path, p+"/") || strings.HasPrefix(path, p+"?") {
				http.NotFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
