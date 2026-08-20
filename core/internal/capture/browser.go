package capture

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

type Runtime struct {
	Root        string
	DriverDir   string
	BrowsersDir string
}

type CaptureResult struct {
	Title           string
	FinalURL        string
	Selector        string
	SelectorMatched bool
}

var ErrBlockedPage = errors.New("la página destino fue bloqueada por el sitio remoto")
var ErrLoginPage = errors.New("la página destino es la pantalla de autenticación de Zajuna")
var ErrChallengePage = errors.New("la página destino pide CAPTCHA o MFA")
var ErrSelectorNotFound = errors.New("el selector requerido no apareció en la página destino")

const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

type CaptureOptions struct {
	Selector        string
	Selectors       []string
	RevealSelectors []string
	HideSelectors   []string
	ViewportWidth   int
	ViewportHeight  int
	FullPage        bool
	LabelHint       string
	OwnerName       string
	Timeout         time.Duration
	RequireSelector bool
	OwnerOnly       bool
}

// BrowserCookie is the cookie shape needed to bridge an authenticated HTTP
// session into an isolated Chromium context. It is never persisted.
type BrowserCookie struct {
	Name     string
	Value    string
	URL      string
	Domain   string
	Path     string
	Secure   bool
	HttpOnly bool
	SameSite string
	Expires  *time.Time
}

func Resolve(executablePath string) Runtime {
	if configured := os.Getenv("ZAJUNA_PLAYWRIGHT_DIR"); configured != "" {
		return newRuntime(configured)
	}
	if executablePath == "" {
		executablePath, _ = os.Executable()
	}
	return newRuntime(filepath.Join(filepath.Dir(executablePath), "playwright"))
}

func newRuntime(root string) Runtime {
	return Runtime{
		Root:        root,
		DriverDir:   filepath.Join(root, "driver"),
		BrowsersDir: filepath.Join(root, "browsers"),
	}
}

func (r Runtime) Installed() bool {
	if r.Root == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(r.DriverDir, "package", "cli.js")); err != nil {
		return false
	}
	entries, err := os.ReadDir(r.BrowsersDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return true
		}
	}
	return false
}

func (r Runtime) Start() (*playwright.Playwright, error) {
	if !r.Installed() {
		return nil, fmt.Errorf("runtime Chromium no instalado en %s; ejecuta npm run browser:install", r.Root)
	}
	if err := os.Setenv("PLAYWRIGHT_DRIVER_PATH", r.DriverDir); err != nil {
		return nil, fmt.Errorf("configurar driver de Playwright: %w", err)
	}
	if err := os.Setenv("PLAYWRIGHT_BROWSERS_PATH", r.BrowsersDir); err != nil {
		return nil, fmt.Errorf("configurar browsers de Playwright: %w", err)
	}
	instance, err := playwright.Run(&playwright.RunOptions{
		DriverDirectory:     r.DriverDir,
		Browsers:            []string{"chromium"},
		SkipInstallBrowsers: true,
		Verbose:             false,
	})
	if err != nil {
		return nil, fmt.Errorf("iniciar Playwright local: %w", err)
	}
	return instance, nil
}

func (r Runtime) CaptureURL(ctx context.Context, targetURL, outputPath string) error {
	_, err := r.CaptureURLWithMetadata(ctx, targetURL, outputPath)
	return err
}

func (r Runtime) CaptureURLWithMetadata(ctx context.Context, targetURL, outputPath string) (CaptureResult, error) {
	return r.CaptureURLWithMetadataAndCookies(ctx, targetURL, outputPath, nil)
}

func (r Runtime) CaptureURLWithMetadataAndCookies(ctx context.Context, targetURL, outputPath string, cookies []BrowserCookie) (CaptureResult, error) {
	return r.CaptureURLWithMetadataAndCookiesAndOptions(ctx, targetURL, outputPath, cookies, CaptureOptions{})
}

func (r Runtime) CaptureURLWithMetadataAndCookiesAndOptions(ctx context.Context, targetURL, outputPath string, cookies []BrowserCookie, options CaptureOptions) (CaptureResult, error) {
	if ctx == nil {
		ctx = context.Background()
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
	pw, err := r.Start()
	if err != nil {
		return CaptureResult{}, err
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		return CaptureResult{}, fmt.Errorf("lanzar Chromium local: %w", err)
	}
	defer browser.Close()
	browserContext, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(browserUserAgent),
		Locale:    playwright.String("es-CO"),
	})
	if err != nil {
		return CaptureResult{}, fmt.Errorf("crear contexto Chromium: %w", err)
	}
	defer browserContext.Close()
	if len(cookies) > 0 {
		browserCookies := make([]playwright.OptionalCookie, 0, len(cookies))
		for _, cookie := range cookies {
			item, ok := cookie.playwrightCookie()
			if !ok {
				continue
			}
			browserCookies = append(browserCookies, item)
		}
		if len(browserCookies) > 0 {
			if err := browserContext.AddCookies(browserCookies); err != nil {
				return CaptureResult{}, fmt.Errorf("cargar sesión autenticada en Chromium: %w", err)
			}
		}
	}
	page, err := browserContext.NewPage()
	if err != nil {
		return CaptureResult{}, fmt.Errorf("crear página Chromium: %w", err)
	}
	return capturePage(ctx, page, targetURL, absoluteOutput, options)
}

func capturePage(ctx context.Context, page playwright.Page, targetURL, absoluteOutput string, options CaptureOptions) (CaptureResult, error) {
	parsedTarget, err := url.Parse(targetURL)
	if err != nil || parsedTarget.Host == "" {
		return CaptureResult{}, errors.New("la URL de captura debe ser http o https")
	}
	if err := ValidateCaptureNavigationURL(targetURL, parsedTarget); err != nil {
		return CaptureResult{}, err
	}
	if options.ViewportWidth > 0 && options.ViewportHeight > 0 {
		if err := page.SetViewportSize(options.ViewportWidth, options.ViewportHeight); err != nil {
			return CaptureResult{}, fmt.Errorf("configurar viewport de captura: %w", err)
		}
	}
	if _, err := page.Goto(targetURL); err != nil {
		return CaptureResult{}, fmt.Errorf("navegar para captura: %w", err)
	}
	_ = page.WaitForLoadState()
	title, _ := page.Title()
	finalURL := page.URL()
	if err := ValidateCaptureNavigationURL(finalURL, parsedTarget); err != nil {
		return CaptureResult{}, fmt.Errorf("captura bloqueada: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return CaptureResult{}, err
	}
	body, _ := page.Locator("body").InnerText()
	if isZajunaLoginPage(finalURL, title, body) {
		return CaptureResult{}, fmt.Errorf("%w: URL final %s", ErrLoginPage, finalURL)
	}
	if isChallengePage(finalURL, title, body) {
		return CaptureResult{}, fmt.Errorf("%w: URL final %s", ErrChallengePage, finalURL)
	}
	if blocked, blockedErr := isBlockedPage(page, title); blocked {
		return CaptureResult{}, blockedErr
	}
	prepareCourseMenu(page, options)
	prepareHiddenEvidenceRegions(page, options.HideSelectors)
	result := CaptureResult{Title: title, FinalURL: finalURL}
	selectors := make([]string, 0, len(options.Selectors)+1)
	addSelector := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range selectors {
			if existing == value {
				return
			}
		}
		selectors = append(selectors, value)
	}
	addSelector(options.Selector)
	for _, selector := range options.Selectors {
		addSelector(selector)
	}
	matchedCandidates := 0
	lastScreenshotError := ""
	selectorDiagnostics := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		locator := page.Locator(selector)
		rawCount, _ := locator.Count()
		if hint := strings.TrimSpace(options.LabelHint); hint != "" {
			filtered := locator.Filter(playwright.LocatorFilterOptions{HasText: hint})
			count, countErr := filtered.Count()
			if countErr != nil || count == 0 {
				selectorDiagnostics = append(selectorDiagnostics, fmt.Sprintf("%s raw=%d hint=0", selector, rawCount))
				// Never use the first generic node when a semantic hint does not
				// match; that would produce unrelated evidence.
				continue
			}
			locator = filtered
		}
		if options.OwnerOnly {
			ownerName := strings.TrimSpace(options.OwnerName)
			if ownerName == "" {
				continue
			}
			filtered := locator.Filter(playwright.LocatorFilterOptions{HasText: ownerName})
			count, countErr := filtered.Count()
			if countErr != nil || count == 0 {
				selectorDiagnostics = append(selectorDiagnostics, fmt.Sprintf("%s raw=%d owner=0", selector, rawCount))
				continue
			}
			locator = filtered
		}
		if options.OwnerOnly {
			selectorDiagnostics = append(selectorDiagnostics, fmt.Sprintf("%s raw=%d owner=%d", selector, rawCount, func() int { value, _ := locator.Count(); return value }()))
		}
		if count, countErr := locator.Count(); countErr == nil && count > 0 {
			matchedCandidates += count
			timeout := 5000.0
			if options.Timeout > 0 {
				timeout = float64(options.Timeout.Milliseconds())
			}
			var captureErr error
			if options.FullPage {
				_, captureErr = page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String(absoluteOutput), FullPage: playwright.Bool(true), Timeout: playwright.Float(timeout)})
			} else {
				_, captureErr = locator.First().Screenshot(playwright.LocatorScreenshotOptions{Path: playwright.String(absoluteOutput), Timeout: playwright.Float(timeout)})
			}
			if captureErr == nil {
				result.Selector = selector
				result.SelectorMatched = true
				return result, nil
			} else {
				lastScreenshotError = captureErr.Error()
			}
		}
	}
	if options.RequireSelector {
		diagnostics := fmt.Sprintf("candidatos=%d", matchedCandidates)
		if len(selectorDiagnostics) > 0 {
			diagnostics += ", selectores=" + strings.Join(selectorDiagnostics, " | ")
		}
		if options.OwnerOnly {
			counts := make([]string, 0, 8)
			for _, diagnosticSelector := range []string{"#region-main", "#page-mod-forum-view", "#region-main table", ".forumheaderlist", ".discussion", ".forumpost", ".forum-post", "article"} {
				count, _ := page.Locator(diagnosticSelector).Count()
				counts = append(counts, fmt.Sprintf("%s=%d", diagnosticSelector, count))
			}
			diagnostics += ", DOM=" + strings.Join(counts, ",")
		}
		if lastScreenshotError != "" {
			diagnostics += fmt.Sprintf(", error de captura: %s", lastScreenshotError)
		}
		return CaptureResult{}, fmt.Errorf("%w: %s (%s)", ErrSelectorNotFound, strings.TrimSpace(options.Selector), diagnostics)
	}
	if _, err := page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String(absoluteOutput), FullPage: playwright.Bool(true)}); err != nil {
		return CaptureResult{}, fmt.Errorf("guardar captura: %w", err)
	}
	if len(selectors) > 0 {
		result.Selector = selectors[0]
	}
	return result, nil
}

func prepareHiddenEvidenceRegions(page playwright.Page, selectors []string) {
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		_, _ = page.Evaluate(`(selector) => {
			for (const node of document.querySelectorAll(selector)) {
				node.setAttribute('data-zajuna-hidden-evidence', 'true');
				node.style.display = 'none';
			}
			return true;
		}`, selector)
	}
}

// prepareCourseMenu gives Moodle's accordion sections a chance to render
// their activity cards before a semantic selector is evaluated. This is
// intentionally limited to explicit collapse controls inside the course
// content; it never clicks activity links or arbitrary page controls.
func prepareCourseMenu(page playwright.Page, options CaptureOptions) {
	selectors := append([]string{options.Selector}, options.Selectors...)
	needsCourseMenu := false
	for _, selector := range selectors {
		if strings.Contains(selector, ".course-content") || strings.Contains(selector, "activity-item") {
			needsCourseMenu = true
			break
		}
	}
	if !needsCourseMenu {
		return
	}
	_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State:   playwright.LoadStateNetworkidle,
		Timeout: playwright.Float(5000),
	})
	if len(options.RevealSelectors) > 0 {
		for _, selector := range options.RevealSelectors {
			control := page.Locator(strings.TrimSpace(selector))
			if count, err := control.Count(); err == nil && count > 0 {
				expanded, _ := control.First().GetAttribute("aria-expanded")
				if strings.EqualFold(strings.TrimSpace(expanded), "false") {
					_ = control.First().Click(playwright.LocatorClickOptions{
						Force:   playwright.Bool(true),
						Timeout: playwright.Float(1000),
					})
				}
				_, _ = page.Evaluate(`(selector) => {
					const control = document.querySelector(selector);
					if (!control) return false;
					const targetID = control.getAttribute('aria-controls') || (control.getAttribute('href') || '').replace(/^#/, '');
					const target = targetID ? document.getElementById(targetID) : null;
					if (!target) return false;
					control.setAttribute('aria-expanded', 'true');
					control.classList.remove('collapsed');
					let node = target;
					while (node && node.id !== 'region-main') {
						node.classList.add('show');
						node.classList.remove('collapse');
						node.removeAttribute('hidden');
						if (getComputedStyle(node).display === 'none') node.style.display = 'block';
						if (getComputedStyle(node).visibility === 'hidden') node.style.visibility = 'visible';
						node = node.parentElement;
					}
					return true;
				}`, strings.TrimSpace(selector))
			}
		}
		page.WaitForTimeout(300)
		return
	}
	collapseSelectors := []string{
		"#region-main .course-content [data-toggle='collapse'][aria-expanded='false']",
		"#region-main .course-content a[aria-expanded='false']",
		"#region-main .course-content [aria-expanded='false'][role='button']",
		"#region-main .course-content [aria-expanded='false'].collapsed",
	}
	clicked := 0
	for round := 0; round < 16 && clicked < 40; round++ {
		openedOne := false
		for _, selector := range collapseSelectors {
			locator := page.Locator(selector)
			count, err := locator.Count()
			if err != nil || count == 0 {
				continue
			}
			for index := 0; index < count && clicked < 40; index++ {
				candidate := locator.Nth(index)
				visible, visibleErr := candidate.IsVisible()
				if visibleErr != nil || !visible {
					continue
				}
				if err := candidate.Click(playwright.LocatorClickOptions{
					Force:   playwright.Bool(true),
					Timeout: playwright.Float(500),
				}); err != nil {
					continue
				}
				clicked++
				openedOne = true
				break
			}
			if openedOne {
				break
			}
		}
		if !openedOne {
			break
		}
		page.WaitForTimeout(100)
	}
	page.WaitForTimeout(350)
}

func isBlockedPage(page playwright.Page, title string) (bool, error) {
	body, err := page.Locator("body").InnerText()
	if err != nil {
		return false, nil
	}
	content := strings.ToLower(strings.TrimSpace(title + "\n" + body))
	if !containsBlockedPageMarkers(content) {
		return false, nil
	}
	return true, fmt.Errorf("%w: respuesta WAF de Zajuna", ErrBlockedPage)
}

func containsBlockedPageMarkers(content string) bool {
	content = strings.ToLower(content)
	return strings.Contains(content, "web page blocked") ||
		(strings.Contains(content, "attack id:") && strings.Contains(content, "message id:"))
}

func (r Runtime) RenderHTMLToPDF(ctx context.Context, htmlContent, outputPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if htmlContent == "" || outputPath == "" {
		return errors.New("HTML y ruta de PDF son obligatorios")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	absoluteOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolver salida PDF: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absoluteOutput), 0o700); err != nil {
		return fmt.Errorf("crear carpeta PDF: %w", err)
	}
	pw, err := r.Start()
	if err != nil {
		return err
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		return fmt.Errorf("lanzar Chromium para PDF: %w", err)
	}
	defer browser.Close()
	browserContext, err := browser.NewContext()
	if err != nil {
		return fmt.Errorf("crear contexto PDF: %w", err)
	}
	defer browserContext.Close()
	page, err := browserContext.NewPage()
	if err != nil {
		return fmt.Errorf("crear página PDF: %w", err)
	}
	if err := page.SetContent(htmlContent); err != nil {
		return fmt.Errorf("preparar contenido PDF: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := page.PDF(playwright.PagePdfOptions{Path: playwright.String(absoluteOutput), Format: playwright.String("A4"), PrintBackground: playwright.Bool(true), Tagged: playwright.Bool(true)}); err != nil {
		return fmt.Errorf("guardar PDF: %w", err)
	}
	return nil
}

func (c BrowserCookie) playwrightCookie() (playwright.OptionalCookie, bool) {
	if strings.TrimSpace(c.Name) == "" {
		return playwright.OptionalCookie{}, false
	}
	item := playwright.OptionalCookie{
		Name:     c.Name,
		Value:    c.Value,
		HttpOnly: playwright.Bool(c.HttpOnly),
		Secure:   playwright.Bool(c.Secure),
	}
	if strings.TrimSpace(c.Domain) != "" && strings.TrimSpace(c.Path) != "" {
		item.Domain = playwright.String(c.Domain)
		item.Path = playwright.String(c.Path)
	} else if strings.TrimSpace(c.URL) != "" {
		item.URL = playwright.String(c.URL)
	} else {
		return playwright.OptionalCookie{}, false
	}
	if c.Expires != nil {
		item.Expires = playwright.Float(float64(c.Expires.Unix()))
	}
	switch strings.ToLower(strings.TrimSpace(c.SameSite)) {
	case "strict":
		item.SameSite = playwright.SameSiteAttributeStrict
	case "none":
		item.SameSite = playwright.SameSiteAttributeNone
	case "lax":
		item.SameSite = playwright.SameSiteAttributeLax
	}
	return item, true
}
