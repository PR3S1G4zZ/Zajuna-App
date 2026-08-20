package capture

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zajuna-app/core/internal/security"
)

// BrowserCookieForTarget converts an in-memory HTTP session cookie into the
// isolated Chromium cookie shape. Domain, path and security attributes are
// preserved; cookies whose domain does not match the allowed capture origin
// are rejected instead of being widened.
func BrowserCookieForTarget(cookie http.Cookie, target *url.URL) (BrowserCookie, error) {
	if strings.TrimSpace(cookie.Name) == "" {
		return BrowserCookie{}, errors.New("la cookie de sesión no tiene nombre")
	}
	if target == nil || strings.TrimSpace(target.Scheme) == "" || strings.TrimSpace(target.Host) == "" {
		return BrowserCookie{}, errors.New("el destino de captura es obligatorio")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return BrowserCookie{}, errors.New("la URL de captura debe ser http o https")
	}
	targetHost := strings.ToLower(target.Hostname())
	cookieDomain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cookie.Domain), "."))
	if cookieDomain != "" && cookieDomain != targetHost {
		return BrowserCookie{}, errors.New("el dominio de la cookie no coincide con el origen permitido")
	}
	if cookie.MaxAge < 0 {
		return BrowserCookie{}, errors.New("la cookie de sesión está expirada")
	}
	path := cookie.Path
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	converted := BrowserCookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		URL:      target.Scheme + "://" + target.Host + path,
		Domain:   targetHost,
		Path:     path,
		Secure:   cookie.Secure,
		HttpOnly: cookie.HttpOnly,
		SameSite: sameSiteName(cookie.SameSite),
	}
	if cookie.MaxAge > 0 {
		expires := time.Now().Add(time.Duration(cookie.MaxAge) * time.Second).UTC()
		converted.Expires = &expires
	} else if !cookie.Expires.IsZero() {
		if cookie.Expires.Before(time.Now()) {
			return BrowserCookie{}, errors.New("la cookie de sesión está expirada")
		}
		expires := cookie.Expires.UTC()
		converted.Expires = &expires
	}
	return converted, nil
}

// ValidateCaptureNavigationURL rejects the final capture URL when it leaves
// the requested origin, uses a disallowed scheme, or lands on loopback/private
// infrastructure that the original target did not already use (fixtures).
func ValidateCaptureNavigationURL(raw string, allowedOrigin *url.URL) error {
	if allowedOrigin == nil || strings.TrimSpace(allowedOrigin.Scheme) == "" || strings.TrimSpace(allowedOrigin.Host) == "" {
		return errors.New("el origen permitido de captura es obligatorio")
	}
	origin := allowedOrigin.Scheme + "://" + allowedOrigin.Host
	parsed, err := security.ValidateHTTPURL(raw, []string{origin}, originAllowsPrivate(allowedOrigin))
	if err != nil {
		return err
	}
	if !security.AllowedOrigin(parsed, []string{origin}) {
		return errors.New("la captura terminó fuera del origen permitido")
	}
	return nil
}

func originAllowsPrivate(origin *url.URL) bool {
	host := origin.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && security.IsPrivateIP(ip)
}

func sameSiteName(mode http.SameSite) string {
	switch mode {
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	case http.SameSiteLaxMode:
		return "Lax"
	default:
		return "Lax"
	}
}
