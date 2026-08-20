package zajuna

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseFichas(t *testing.T) {
	body := `<html><title>Mis cursos | Zajuna</title><a href="/zajuna/course/view.php?id=41080">Programa de formación (3135429)</a><a href="/zajuna/course/view.php?id=41081">P_2281_V_3135430</a><a href="/zajuna/course/view.php?id=41080">Duplicada (3135429)</a></html>`
	fichas := parseFichas(body)
	if len(fichas) != 2 {
		t.Fatalf("expected 2 fichas, got %d: %#v", len(fichas), fichas)
	}
	if fichas[0].ExternalID != "3135429" || fichas[0].CourseID != "41080" {
		t.Fatalf("unexpected first ficha: %#v", fichas[0])
	}
	if fichas[1].ExternalID != "3135430" {
		t.Fatalf("unexpected second ficha: %#v", fichas[1])
	}
}

func TestParseProfileName(t *testing.T) {
	body := `<div class="usermenu"><a class="usertext" href="/zajuna/user/profile.php?id=7">Ada Lovelace</a></div>`
	if got := parseProfileName(body); got != "Ada Lovelace" {
		t.Fatalf("unexpected profile name: %q", got)
	}
	if got := parseProfileName(`<span class="usertext">Mis cursos</span>`); got != "" {
		t.Fatalf("navigation label must not be used as profile name: %q", got)
	}
}

func TestHiddenValue(t *testing.T) {
	body := `<input type="hidden" name="josso" value="abc&amp;123"><input name="logintoken" value="token">`
	if got := hiddenValue(body, "josso"); got != "abc&123" {
		t.Fatalf("unexpected hidden value: %q", got)
	}
}

func TestHiddenValueSupportsAttributeOrderAndUnquotedValues(t *testing.T) {
	body := `<input value="rotated-token" type="hidden" name="logintoken"><input value=legacy-token name=josso>`
	if got := hiddenValue(body, "logintoken"); got != "rotated-token" {
		t.Fatalf("unexpected token with reordered attributes: %q", got)
	}
	if got := hiddenValue(body, "josso"); got != "legacy-token" {
		t.Fatalf("unexpected unquoted token: %q", got)
	}
}

func TestLoginAndListFichasWithLocalFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/controllers/login_user/lg.php":
			if r.Method != http.MethodPost {
				t.Errorf("singIn method = %s", r.Method)
			}
			_ = r.ParseForm()
			if r.Form.Get("document") != "123456" || r.Form.Get("password") != "fixture-secret" {
				t.Errorf("singIn credentials were not submitted as expected")
			}
			http.SetCookie(w, &http.Cookie{Name: "prelogin", Value: "1", Path: "/"})
			// Current Zajuna responses may omit josso and expose only
			// logintoken, with the attributes in a different order.
			_, _ = w.Write([]byte(`<input value="fixture-token" type="hidden" name="logintoken"><input type="hidden" name="username" value="123456"><input type="hidden" name="password" value="fixture-secret">`))
		case "/zajuna/login/index.php":
			if r.Method != http.MethodPost {
				t.Errorf("login method = %s", r.Method)
			}
			_ = r.ParseForm()
			if r.Form.Get("josso") != "" || r.Form.Get("logintoken") != "fixture-token" {
				t.Errorf("login tokens were not submitted as expected")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "valid", Path: "/"})
			_, _ = w.Write([]byte(`<meta http-equiv="refresh" content="0; url=/zajuna/my/courses.php?testsession=1">`))
		case "/zajuna/my/courses.php":
			if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "valid" {
				w.Header().Set("Location", "/zajuna/login/index.php")
				w.WriteHeader(http.StatusFound)
				return
			}
			_, _ = w.Write([]byte(`<html><title>Mis cursos | Zajuna</title><a href="/zajuna/course/view.php?id=41080">Programa demo (3135429)</a><a href="/zajuna/course/view.php?id=41081">P_2281_V_3135430</a></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Login(context.Background(), Credentials{DocumentType: "CC", Document: "123456", Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	fichas, err := client.ListFichas(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(fichas) != 2 || fichas[0].ExternalID != "3135429" {
		t.Fatalf("unexpected fichas: %#v", fichas)
	}
	cookies := session.Client.Jar.Cookies(&url.URL{Scheme: "http", Host: server.Listener.Addr().String(), Path: "/zajuna/my/courses.php"})
	if len(cookies) == 0 {
		t.Fatal("expected authenticated session cookies")
	}
}

func TestLoginSupportsCurrentDirectRedirectFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/controllers/login_user/lg.php":
			if r.Method != http.MethodPost {
				t.Errorf("direct login method = %s", r.Method)
			}
			_ = r.ParseForm()
			if r.Form.Get("typeDocument") != "CC" || r.Form.Get("document") != "123456" || r.Form.Get("password") != "fixture-secret" {
				t.Errorf("direct login credentials were not submitted as expected")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "valid", Path: "/"})
			http.Redirect(w, r, "/zajuna/my/courses.php", http.StatusFound)
		case "/zajuna/my/courses.php":
			if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "valid" {
				http.Error(w, "login", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`<html><title>Mis cursos | Zajuna</title><a href="/zajuna/course/view.php?id=41080">Curso demo (3135429)</a></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Login(context.Background(), Credentials{DocumentType: "CC", Document: "123456", Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	fichas, err := client.ListFichas(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(fichas) != 1 || fichas[0].ExternalID != "3135429" {
		t.Fatalf("unexpected fichas from direct login flow: %#v", fichas)
	}
}

func TestDiscoverCourseMapWithLocalFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/controllers/login_user/lg.php" {
			_, _ = w.Write([]byte(`<input type="hidden" name="josso" value="fixture-josso"><input type="hidden" name="logintoken" value="fixture-token">`))
			return
		}
		if r.URL.Path == "/zajuna/login/index.php" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "valid", Path: "/"})
			_, _ = w.Write([]byte(`<meta http-equiv="refresh" content="0; url=/zajuna/my/courses.php">`))
			return
		}
		if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "valid" {
			http.Error(w, "login", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/zajuna/my/courses.php":
			_, _ = w.Write([]byte(`<html><title>Mis cursos | Zajuna</title><a href="/zajuna/course/view.php?id=41080">Curso demo</a></html>`))
		case "/zajuna/course/view.php":
			if r.URL.Query().Get("id") == "41081" {
				_, _ = w.Write([]byte(`<title>Curso relacionado</title><a href="/zajuna/mod/forum/view.php?id=9">Foro relacionado</a>`))
				return
			}
			_, _ = w.Write([]byte(`<title>Curso demo</title>
				<li id="section-3"><h3 class="sectionname">FASE 1 PLANEAR</h3>
					<li class="activity" data-modulename="label"><span class="instancename">ACTIVIDAD DE PROYECTO 1</span></li>
					<div class="activityname"><span class="instancename">GA1-220501044-AA1-EV01</span><a href="/zajuna/mod/assign/view.php?id=31">Entrega</a></div>
				</li>
				<li id="section-4"><h3 class="sectionname">FASE 2 HACER</h3></li>
				<a href="/zajuna/mod/forum/view.php?id=1">Foro</a>
				<a href="/zajuna/mod/page/view.php?id=2">Página</a>
				<a href="/zajuna/mod/assign/view.php?id=3">Tarea</a>
				<a href="/zajuna/mod/resource/view.php?id=4">Recurso</a>
				<a href="/zajuna/mod/url/view.php?id=5">Enlace</a>
				<a href="/zajuna/course/view.php?id=41081">Otro curso</a>
				<a href="https://externo.example/should-not-follow">Externo</a>`))
		case "/zajuna/mod/forum/view.php":
			_, _ = w.Write([]byte(`<title>Foro</title><a href="/zajuna/mod/page/view.php?id=7">Página del foro</a>`))
		case "/zajuna/mod/page/view.php":
			_, _ = w.Write([]byte(`<title>Página</title><a href="/zajuna/mod/assign/view.php?id=8">Tarea de página</a><a href="/zajuna/pluginfile.php/1/demo.pdf">PDF</a>`))
		case "/zajuna/mod/assign/view.php", "/zajuna/mod/resource/view.php", "/zajuna/mod/url/view.php", "/zajuna/pluginfile.php":
			_, _ = w.Write([]byte(`<title>Detalle</title>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newClient(server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Login(context.Background(), Credentials{DocumentType: "CC", Document: "123456", Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := client.DiscoverCourseMap(context.Background(), session, "41080", CrawlOptions{MaxDepth: 2, MaxPages: 20, MaxLinksPerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if record.LinkCount < 12 || record.ScrapeStats.Phases < 2 || record.ScrapeStats.Forums < 1 || record.ScrapeStats.Pages < 1 || record.ScrapeStats.Assigns < 1 || record.ScrapeStats.Gradings < 1 || record.ScrapeStats.Resources < 1 || record.ScrapeStats.URLs < 1 {
		t.Fatalf("unexpected course map stats: %#v", record)
	}
	var foundActivity bool
	for _, route := range record.Routes {
		if route.ActivityID == "31" {
			foundActivity = true
			if route.PhaseName != "FASE 1 PLANEAR" || route.Subsection == "" || !route.Technical {
				t.Fatalf("activity metadata was not preserved: %#v", route)
			}
		}
	}
	if !foundActivity {
		t.Fatal("expected assignment route from course structure")
	}
	for _, route := range record.Routes {
		if strings.Contains(route.URL, "externo.example") {
			t.Fatalf("external route should not be included: %#v", route)
		}
	}
	if record.ByItemCode["route.forum"] == nil || record.ByItemCode["route.page"] == nil {
		t.Fatalf("expected grouped route categories: %#v", record.ByItemCode)
	}
}

func TestLoginRejectsCaptchaChallengeWithoutAutomatingIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/controllers/login_user/lg.php" {
			_, _ = w.Write([]byte(`<html><body><div class="g-recaptcha" data-sitekey="fixture"></div><p>Complete el captcha</p></body></html>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client, err := newClient(server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Login(context.Background(), Credentials{DocumentType: "CC", Document: "123456", Password: "fixture-secret"})
	if !errors.Is(err, ErrChallengeRequired) {
		t.Fatalf("expected challenge error, got %v", err)
	}
}

func TestListFichasTreatsLoginPageAsExpiredSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<form class="login__form-cursos"><input name="form_login_user"></form>`))
	}))
	defer server.Close()
	client, err := newClient(server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	session := Session{Client: server.Client(), BaseURL: server.URL}
	_, err = client.ListFichas(context.Background(), session)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected expired session, got %v", err)
	}
}
