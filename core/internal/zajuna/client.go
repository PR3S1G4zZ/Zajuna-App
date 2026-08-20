package zajuna

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zajuna-app/core/internal/security"
)

const DefaultBaseURL = "https://zajuna.sena.edu.co"

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var ErrAuthentication = errors.New("autenticación de Zajuna rechazada")
var ErrSessionExpired = errors.New("sesión de Zajuna vencida")
var ErrChallengeRequired = errors.New("Zajuna requiere intervención humana (CAPTCHA o MFA)")

type HTTPStatusError struct {
	StatusCode int
	URL        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("Zajuna respondió HTTP %d en %s", e.StatusCode, security.RedactURL(e.URL))
}

type Credentials struct {
	DocumentType string
	Document     string
	Password     string
}

type Session struct {
	Client      *http.Client
	BaseURL     string
	ProfileName string
}

// CookiesForURL returns a copy of the in-memory session cookies that match a
// target URL. Callers may bridge them into an isolated browser context, but
// must not persist or log them.
func (s Session) CookiesForURL(rawURL string) []http.Cookie {
	if s.Client == nil || s.Client.Jar == nil {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return nil
	}
	items := s.Client.Jar.Cookies(parsed)
	result := make([]http.Cookie, 0, len(items))
	for _, item := range items {
		if item != nil {
			result = append(result, *item)
		}
	}
	return result
}

type Ficha struct {
	ExternalID string `json:"externalId"`
	Name       string `json:"name"`
	CourseID   string `json:"courseId"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
}

func NewClient(baseURL string) (*Client, error) {
	return newClient(baseURL, false)
}

func newClient(baseURL string, allowHTTP bool) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, errors.New("la URL base de Zajuna debe usar HTTPS")
	}
	validScheme := parsed.Scheme == "https" || (allowHTTP && parsed.Scheme == "http")
	if !validScheme || parsed.Host == "" {
		return nil, errors.New("la URL base de Zajuna debe usar HTTPS")
	}
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: defaultUserAgent,
	}, nil
}

func (c *Client) Login(ctx context.Context, credentials Credentials) (Session, error) {
	if credentials.Document == "" || credentials.Password == "" {
		return Session{}, errors.New("faltan credenciales de Zajuna")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return Session{}, fmt.Errorf("crear almacén de cookies: %w", err)
	}
	httpClient := &http.Client{
		Jar:     jar,
		Timeout: 45 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	session := Session{Client: httpClient, BaseURL: c.baseURL}

	form := url.Values{
		"typeDocument":    {defaultDocumentType(credentials.DocumentType)},
		"document":        {credentials.Document},
		"password":        {credentials.Password},
		"form_login_user": {"Iniciar sesión"},
	}
	// The live Zajuna login form currently posts credentials to lg.php. The
	// older singIn.php route can return a shell without the initial token,
	// which makes the second login request impossible.
	initialResponse, err := c.doFormResponse(ctx, session, "/controllers/login_user/lg.php", form, "/zajuna/login/index.php")
	if err != nil {
		return Session{}, err
	}
	initialBody, err := readBody(initialResponse)
	location := initialResponse.Header.Get("Location")
	initialResponse.Body.Close()
	if err != nil {
		return Session{}, err
	}
	if looksLikeChallengePage(initialBody) {
		return Session{}, ErrChallengeRequired
	}
	if initialResponse.StatusCode >= 400 {
		return Session{}, fmt.Errorf("%w: login HTTP %d", ErrAuthentication, initialResponse.StatusCode)
	}

	// The current learner form authenticates directly and redirects. Older
	// deployments returned an intermediate form with logintoken/josso, so keep
	// that compatibility path without requiring tokens from the direct flow.
	if loginToken := hiddenValue(initialBody, "logintoken"); loginToken != "" {
		loginForm := url.Values{
			"logintoken":   {loginToken},
			"typeDocument": {defaultDocumentType(credentials.DocumentType)},
			"username":     {firstNonEmpty(hiddenValue(initialBody, "username"), credentials.Document)},
			"password":     {firstNonEmpty(hiddenValue(initialBody, "password"), credentials.Password)},
		}
		if josso := hiddenValue(initialBody, "josso"); josso != "" {
			loginForm.Set("josso", josso)
		}
		response, err := c.doFormResponse(ctx, session, "/zajuna/login/index.php", loginForm, "/controllers/login_user/lg.php")
		if err != nil {
			return Session{}, err
		}
		if response.StatusCode >= 400 {
			response.Body.Close()
			return Session{}, fmt.Errorf("%w: login HTTP %d", ErrAuthentication, response.StatusCode)
		}
		loginBody, err := readBody(response)
		if location == "" {
			location = response.Header.Get("Location")
		}
		response.Body.Close()
		if err != nil {
			return Session{}, err
		}
		if looksLikeChallengePage(loginBody) {
			return Session{}, ErrChallengeRequired
		}
		if location == "" {
			location = metaRedirect(loginBody)
		}
	} else if location == "" {
		location = metaRedirect(initialBody)
	}
	if location != "" {
		resolved, err := resolveURL(c.baseURL, location)
		if err != nil {
			return Session{}, fmt.Errorf("redirección de login inválida: %w", err)
		}
		if _, err := c.doGet(ctx, session, resolved, "/zajuna/login/index.php"); err != nil {
			return Session{}, err
		}
	}
	coursesBody, err := c.doGet(ctx, session, c.baseURL+"/zajuna/my/courses.php", "/zajuna/login/index.php")
	if err != nil {
		return Session{}, err
	}
	if looksLikeLoginPage(coursesBody) {
		return Session{}, fmt.Errorf("%w: la sesión no quedó autenticada", ErrAuthentication)
	}
	if len(session.Client.Jar.Cookies(mustURL(c.baseURL+"/zajuna/my/courses.php"))) == 0 {
		return Session{}, fmt.Errorf("%w: Zajuna no devolvió cookies de sesión", ErrAuthentication)
	}
	session.ProfileName = parseProfileName(coursesBody)
	return session, nil
}

func (c *Client) ListFichas(ctx context.Context, session Session) ([]Ficha, error) {
	if session.Client == nil {
		return nil, ErrSessionExpired
	}
	htmlBody, err := c.doGet(ctx, session, c.baseURL+"/zajuna/my/courses.php", "/zajuna/login/index.php")
	if err != nil {
		return nil, err
	}
	if looksLikeLoginPage(htmlBody) || !strings.Contains(strings.ToLower(htmlBody), "mis cursos") {
		if looksLikeLoginPage(htmlBody) {
			return nil, fmt.Errorf("%w: la sesión no tiene acceso a Mis cursos", ErrSessionExpired)
		}
		return nil, errors.New("la sesión de Zajuna no tiene acceso a Mis cursos")
	}
	fichas := parseFichas(htmlBody)
	if len(fichas) == 0 {
		return nil, errors.New("no se encontraron fichas en Mis cursos")
	}
	return fichas, nil
}

func (c *Client) doForm(ctx context.Context, session Session, path string, form url.Values, referer string) (string, error) {
	response, err := c.doFormResponse(ctx, session, path, form, referer)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	return readBody(response)
}

func (c *Client) doFormResponse(ctx context.Context, session Session, path string, form url.Values, referer string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.absolute(path), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("User-Agent", c.userAgent)
	if referer != "" {
		request.Header.Set("Referer", c.absolute(referer))
		request.Header.Set("Origin", c.baseURL)
	}
	response, err := session.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("conectar con Zajuna: %w", err)
	}
	return response, nil
}

func (c *Client) doGet(ctx context.Context, session Session, target string, referer string) (string, error) {
	allowPrivate := strings.HasPrefix(strings.ToLower(c.baseURL), "http://127.0.0.1") || strings.HasPrefix(strings.ToLower(c.baseURL), "http://localhost")
	if _, err := security.ValidateHTTPURL(target, []string{c.baseURL}, allowPrivate); err != nil {
		return "", fmt.Errorf("destino de Zajuna no permitido: %w", err)
	}
	currentURL := target
	currentReferer := referer
	for redirects := 0; redirects < 6; redirects++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return "", err
		}
		request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		request.Header.Set("User-Agent", c.userAgent)
		if currentReferer != "" {
			request.Header.Set("Referer", c.absolute(currentReferer))
		}
		response, err := session.Client.Do(request)
		if err != nil {
			return "", fmt.Errorf("conectar con Zajuna: %w", err)
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Header.Get("Location")
			response.Body.Close()
			if location == "" {
				return "", fmt.Errorf("Zajuna respondió HTTP %d sin redirección", response.StatusCode)
			}
			resolved, err := resolveURL(currentURL, location)
			if err != nil {
				return "", fmt.Errorf("redirección de Zajuna inválida: %w", err)
			}
			if _, err := security.ValidateHTTPURL(resolved, []string{c.baseURL}, allowPrivate); err != nil {
				return "", fmt.Errorf("redirección de Zajuna fuera del origen permitido: %w", err)
			}
			currentReferer = currentURL
			currentURL = resolved
			continue
		}
		defer response.Body.Close()
		if response.StatusCode >= 400 {
			return "", &HTTPStatusError{StatusCode: response.StatusCode, URL: currentURL}
		}
		return readBody(response)
	}
	return "", errors.New("Zajuna excedió el límite de redirecciones")
}

func (c *Client) absolute(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return c.baseURL + "/" + strings.TrimLeft(path, "/")
}

func readBody(response *http.Response) (string, error) {
	contents, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return "", fmt.Errorf("leer respuesta de Zajuna: %w", err)
	}
	return string(contents), nil
}

var inputTagPattern = regexp.MustCompile(`(?is)<input\b[^>]*>`)
var inputAttributePattern = regexp.MustCompile(`(?is)([a-z_:][a-z0-9_.:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

func hiddenValue(body string, wanted string) string {
	for _, tag := range inputTagPattern.FindAllString(body, -1) {
		attributes := map[string]string{}
		for _, match := range inputAttributePattern.FindAllStringSubmatch(tag, -1) {
			value := match[2]
			if value == "" {
				value = match[3]
			}
			if value == "" {
				value = match[4]
			}
			attributes[strings.ToLower(match[1])] = html.UnescapeString(value)
		}
		if strings.EqualFold(attributes["name"], wanted) {
			return attributes["value"]
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var metaRedirectPattern = regexp.MustCompile(`(?is)(?:url|location)\s*=\s*["']?([^"'\s>]+)`)

func metaRedirect(body string) string {
	match := metaRedirectPattern.FindStringSubmatch(body)
	if len(match) == 2 {
		return html.UnescapeString(match[1])
	}
	return ""
}

func looksLikeLoginPage(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "login__form-cursos") || strings.Contains(lower, "form_login_user")
}

func looksLikeChallengePage(body string) bool {
	lower := strings.ToLower(body)
	markers := []string{
		"g-recaptcha",
		"h-captcha",
		"hcaptcha",
		"recaptcha",
		"captcha",
		"autenticación de dos factores",
		"autenticacion de dos factores",
		"two-factor",
		"two factor",
		"código de verificación",
		"codigo de verificacion",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func mustURL(raw string) *url.URL {
	parsed, _ := url.Parse(raw)
	return parsed
}

var courseLinkPattern = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']*course/view\.php\?id=(\d+)[^"']*)["'][^>]*>(.*?)</a>`)
var tagPattern = regexp.MustCompile(`(?s)<[^>]+>`)
var fichaCodePattern = regexp.MustCompile(`(?i)\((\d{6,9})\)|P_\d+_V_(\d{6,9})`)
var profileNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<[^>]*class=["'][^"']*\busertext\b[^"']*["'][^>]*>(.*?)</[^>]+>`),
	regexp.MustCompile(`(?is)<a[^>]+href=["'][^"']*/user/profile\.php[^"']*["'][^>]*>(.*?)</a>`),
	regexp.MustCompile(`(?is)<[^>]*class=["'][^"']*\bfullname\b[^"']*["'][^>]*>(.*?)</[^>]+>`),
}

func parseProfileName(body string) string {
	for _, pattern := range profileNamePatterns {
		for _, match := range pattern.FindAllStringSubmatch(body, -1) {
			if len(match) < 2 {
				continue
			}
			name := cleanText(match[1])
			words := strings.Fields(name)
			if len(words) >= 2 && len([]rune(name)) <= 120 && !strings.Contains(strings.ToLower(name), "mis cursos") {
				return name
			}
		}
	}
	return ""
}

func parseFichas(body string) []Ficha {
	seen := map[string]bool{}
	result := make([]Ficha, 0)
	for _, match := range courseLinkPattern.FindAllStringSubmatch(body, -1) {
		courseID := match[2]
		name := cleanText(match[3])
		if name == "" || len(name) < 3 || strings.Contains(strings.ToLower(name), "mis cursos") {
			continue
		}
		code := courseID
		if codeMatch := fichaCodePattern.FindStringSubmatch(name); codeMatch != nil {
			if codeMatch[1] != "" {
				code = codeMatch[1]
			} else if codeMatch[2] != "" {
				code = codeMatch[2]
			}
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		result = append(result, Ficha{ExternalID: code, Name: formatFichaName(name, code), CourseID: courseID})
	}
	return result
}

func cleanText(value string) string {
	value = tagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.Join(strings.Fields(value), " ")
}

func formatFichaName(value, code string) string {
	value = regexp.MustCompile(`\s*\(\d{6,9}\)\s*$`).ReplaceAllString(value, "")
	if strings.HasPrefix(strings.ToUpper(value), "P_") {
		return "Ficha " + code
	}
	return strings.TrimSpace(value)
}

func defaultDocumentType(value string) string {
	if strings.TrimSpace(value) == "" {
		return "CC"
	}
	return value
}

func resolveURL(base string, location string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	locationURL, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(locationURL).String(), nil
}
