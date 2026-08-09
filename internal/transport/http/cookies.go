package httptransport

import (
	"net/http"
	"time"

	"github.com/hprotzek/einkaufsliste/internal/auth"
)

const (
	// refreshCookieName is the only place a refresh token ever lives in the
	// browser. Never localStorage, and never a response body — the whole
	// point is that JavaScript cannot read it (non-negotiable 6).
	refreshCookieName = "refresh_token"

	// refreshCookiePath scopes the cookie to the endpoints that need it, so
	// it is not attached to every list request. A credential that travels on
	// requests which do not need it is a credential with more chances to
	// leak.
	refreshCookiePath = "/api/v1/auth"
)

// setRefreshCookie stores a refresh token in the browser.
//
// SameSite=Lax rather than Strict: the invite flow (§9) sends people back
// from a provider redirect, and Strict would drop the cookie on that
// navigation. Lax still refuses it on cross-site POSTs, which is the case
// that matters.
func (s server) setRefreshCookie(w http.ResponseWriter, token auth.RefreshToken) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token.Token,
		Path:     refreshCookiePath,
		Expires:  token.ExpiresAt,
		MaxAge:   int(time.Until(token.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearRefreshCookie expires the cookie. Every attribute except the value
// must match the one that set it, or the browser keeps the original.
func (s server) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// refreshCookie reads the presented refresh token, if there is one.
func refreshCookie(r *http.Request) string {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
