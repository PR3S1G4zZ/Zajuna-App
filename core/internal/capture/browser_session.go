package capture

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

// BrowserCredentials are used only to establish an ephemeral browser session.
// They are never persisted, logged, or returned by the API.
type BrowserCredentials struct {
	LoginURL     string
	DocumentType string
	Document     string
	Password     string
}

// BrowserSession keeps one Chromium context for a batch of captures. Reusing
// the page preserves the browser-authenticated session and avoids starting a
// new headless browser for every checklist slot.
type BrowserSession struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	context playwright.BrowserContext
	page    playwright.Page
}

func (r Runtime) OpenBrowserSession(ctx context.Context, credentials BrowserCredentials) (*BrowserSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(credentials.LoginURL) == "" || credentials.Document == "" || credentials.Password == "" {
		return nil, errors.New("la sesión Chromium requiere URL de login y credenciales")
	}
	loginURL, err := url.Parse(credentials.LoginURL)
	if err != nil || loginURL.Scheme != "https" || loginURL.Host == "" {
		return nil, errors.New("la URL de login de Zajuna debe usar HTTPS")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	pw, err := r.Start()
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = pw.Stop()
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("lanzar Chromium local: %w", err)
	}
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(browserUserAgent),
		Locale:    playwright.String("es-CO"),
	})
	if err != nil {
		_ = browser.Close()
		cleanup()
		return nil, fmt.Errorf("crear contexto Chromium: %w", err)
	}
	page, err := context.NewPage()
	if err != nil {
		_ = context.Close()
		_ = browser.Close()
		cleanup()
		return nil, fmt.Errorf("crear página Chromium: %w", err)
	}
	session := &BrowserSession{pw: pw, browser: browser, context: context, page: page}
	if _, err := page.Goto(credentials.LoginURL); err != nil {
		session.Close()
		return nil, fmt.Errorf("abrir login de Zajuna en Chromium: %w", err)
	}
	if blocked, blockedErr := isBlockedPage(page, pageTitle(page)); blocked {
		session.Close()
		return nil, blockedErr
	}
	loginBody, _ := page.Locator("body").InnerText()
	if isChallengePage(page.URL(), pageTitle(page), loginBody) {
		session.Close()
		return nil, ErrChallengePage
	}
	loginForm := learnerLoginForm(page)
	if err := selectDocumentType(loginForm, credentials.DocumentType); err != nil {
		session.Close()
		return nil, fmt.Errorf("seleccionar tipo de documento en Zajuna: %w", err)
	}
	if err := loginForm.Locator(`input[name="document"]`).Fill(credentials.Document); err != nil {
		session.Close()
		return nil, fmt.Errorf("completar documento en Zajuna: %w", err)
	}
	if err := loginForm.Locator(`input[name="password"]`).Fill(credentials.Password); err != nil {
		session.Close()
		return nil, fmt.Errorf("completar contraseña en Zajuna: %w", err)
	}
	submit := loginForm.Locator(`input[type="submit"], button`).First()
	if err := submit.Click(); err != nil {
		session.Close()
		return nil, fmt.Errorf("enviar login de Zajuna: %w", err)
	}
	if err := page.WaitForLoadState(); err != nil {
		session.Close()
		return nil, fmt.Errorf("esperar respuesta de login de Zajuna: %w", err)
	}
	if blocked, blockedErr := isBlockedPage(page, pageTitle(page)); blocked {
		session.Close()
		return nil, blockedErr
	}
	body, _ := page.Locator("body").InnerText()
	if isChallengePage(page.URL(), pageTitle(page), body) {
		session.Close()
		return nil, ErrChallengePage
	}
	if isZajunaLoginPage(page.URL(), pageTitle(page), body) {
		session.Close()
		return nil, errors.New("la sesión de Chromium no quedó autenticada en Zajuna")
	}
	return session, nil
}

func (s *BrowserSession) CaptureURLWithMetadataAndOptions(ctx context.Context, targetURL, outputPath string, options CaptureOptions) (CaptureResult, error) {
	if s == nil || s.page == nil {
		return CaptureResult{}, errors.New("la sesión Chromium no está disponible")
	}
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed.Host == "" {
		return CaptureResult{}, errors.New("la URL de captura debe ser http o https")
	}
	if err := ValidateCaptureNavigationURL(targetURL, parsed); err != nil {
		return CaptureResult{}, err
	}
	if outputPath == "" {
		return CaptureResult{}, errors.New("la ruta de salida de captura es obligatoria")
	}
	if err := ctx.Err(); err != nil {
		return CaptureResult{}, err
	}
	absoluteOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("resolver salida de captura: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absoluteOutput), 0o700); err != nil {
		return CaptureResult{}, fmt.Errorf("crear carpeta de captura: %w", err)
	}
	return capturePage(ctx, s.page, targetURL, absoluteOutput, options)
}

// AuthenticatedOwnerName reads the current instructor identity from Zajuna's
// authenticated profile. The name is used only in-memory to scope forum and
// announcement evidence to the logged-in instructor; it is never persisted as
// a credential or written to logs.
func (s *BrowserSession) AuthenticatedOwnerName(ctx context.Context, profileURL string) (string, error) {
	if s == nil || s.page == nil {
		return "", errors.New("la sesión Chromium no está disponible")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if name := pageOwnerName(s.page); name != "" {
		return name, nil
	}
	if strings.TrimSpace(profileURL) == "" {
		return "", errors.New("Zajuna no mostró el nombre del instructor autenticado")
	}
	if _, err := s.page.Goto(profileURL); err != nil {
		return "", fmt.Errorf("abrir perfil autenticado de Zajuna: %w", err)
	}
	_ = s.page.WaitForLoadState()
	if blocked, blockedErr := isBlockedPage(s.page, pageTitle(s.page)); blocked {
		return "", blockedErr
	}
	body, _ := s.page.Locator("body").InnerText()
	if isZajunaLoginPage(s.page.URL(), pageTitle(s.page), body) {
		return "", errors.New("la sesión de Chromium expiró al abrir el perfil del instructor")
	}
	if name := pageOwnerName(s.page); name != "" {
		return name, nil
	}
	return "", errors.New("Zajuna no mostró el nombre del instructor autenticado")
}

func pageOwnerName(page playwright.Page) string {
	selectors := []string{
		".usermenu .userbutton .usertext",
		".logininfo a[href*='/user/profile.php']",
		"#page-user-profile .page-header-headings h1",
		"#page-user-profile .page-header-headings h2",
		"#page-user-profile h2",
		"#page-user-profile h1",
		".userprofile .fullname",
	}
	for _, selector := range selectors {
		locator := page.Locator(selector).First()
		count, err := locator.Count()
		if err != nil || count == 0 {
			continue
		}
		text, err := locator.InnerText()
		if err != nil {
			continue
		}
		if cleaned := cleanOwnerName(text); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func cleanOwnerName(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || len([]rune(value)) > 120 || len(strings.Fields(value)) < 2 {
		return ""
	}
	return value
}

func (s *BrowserSession) Close() {
	if s == nil {
		return
	}
	if s.context != nil {
		_ = s.context.Close()
		s.context = nil
	}
	if s.browser != nil {
		_ = s.browser.Close()
		s.browser = nil
	}
	if s.pw != nil {
		_ = s.pw.Stop()
		s.pw = nil
	}
}

func learnerLoginForm(page playwright.Page) playwright.Locator {
	form := page.Locator(`form[action*="login_user"]`)
	if count, err := form.Count(); err == nil && count > 0 {
		return form.First()
	}
	return page.Locator(`form:has(input[name="document"])`).First()
}

func selectDocumentType(form playwright.Locator, documentType string) error {
	selectLocator := form.Locator(`select[name="typeDocument"]`)
	if count, err := selectLocator.Count(); err != nil || count == 0 {
		selectLocator = form.Locator("select").First()
	}
	documentType = strings.ToUpper(strings.TrimSpace(documentType))
	if documentType == "" {
		documentType = "CC"
	}
	values := []string{documentType}
	if _, err := selectLocator.SelectOption(playwright.SelectOptionValues{Values: &values}); err == nil {
		return nil
	}
	labelsByType := map[string]string{
		"CC": "Cédula de Ciudadanía",
		"TI": "Tarjeta de Identidad",
		"CE": "Cédula de Extranjería",
	}
	label := labelsByType[documentType]
	if label == "" {
		return fmt.Errorf("tipo de documento no soportado: %s", documentType)
	}
	labels := []string{label}
	_, err := selectLocator.SelectOption(playwright.SelectOptionValues{Labels: &labels})
	return err
}

func pageTitle(page playwright.Page) string {
	title, _ := page.Title()
	return title
}

func isZajunaLoginPage(rawURL, title, body string) bool {
	lowerURL := strings.ToLower(rawURL)
	lowerBody := strings.ToLower(title + "\n" + body)
	if strings.Contains(lowerURL, "/login/") || strings.Contains(lowerURL, "login_user") {
		return true
	}
	return strings.Contains(lowerBody, "número de documento") &&
		strings.Contains(lowerBody, "contraseña") &&
		strings.Contains(lowerBody, "iniciar sesión")
}

func isChallengePage(rawURL, title, body string) bool {
	content := strings.ToLower(rawURL + "\n" + title + "\n" + body)
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
		if strings.Contains(content, marker) {
			return true
		}
	}
	return false
}
