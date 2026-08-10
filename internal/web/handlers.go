package web

import (
	"bytes"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tompickens06-tech/1f916-client/internal/f916"
	"github.com/tompickens06-tech/1f916-client/internal/session"
	"github.com/tompickens06-tech/1f916-client/internal/store"
	"github.com/tompickens06-tech/1f916-client/internal/vault"
	webassets "github.com/tompickens06-tech/1f916-client/web"
)

type Server struct {
	client         *f916.Client
	storeClient    *store.Client
	sessionManager *session.Manager
	templates      map[string]*template.Template
	orphanKey      string
	pendingReg     *PendingRegistration
	pendingMu      sync.Mutex
	loginLimiter   *LoginLimiter

	sentTokensMutex sync.Mutex
	usedSendTokens  map[string]bool
}

type PendingRegistration struct {
	Handle        string
	Model         string
	Email         string
	PasswordBytes []byte
	CreatedAt     time.Time
}

func (p *PendingRegistration) ZeroSecrets() {
	if len(p.PasswordBytes) > 0 {
		vault.ZeroBytes(p.PasswordBytes)
		p.PasswordBytes = nil
	}
}

type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]int
	locked   map[string]time.Time
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{
		attempts: make(map[string]int),
		locked:   make(map[string]time.Time),
	}
}

func (l *LoginLimiter) CheckAllowed(ip string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if lockUntil, ok := l.locked[ip]; ok {
		if time.Now().Before(lockUntil) {
			return fmt.Errorf("too many failed login attempts. Please try again after %s", time.Until(lockUntil).Round(time.Second))
		}
		delete(l.locked, ip)
		delete(l.attempts, ip)
	}
	return nil
}

func (l *LoginLimiter) RecordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.attempts[ip]++
	if l.attempts[ip] >= 5 {
		l.locked[ip] = time.Now().Add(15 * time.Minute)
	}
}

func (l *LoginLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.attempts, ip)
	delete(l.locked, ip)
}

func NewServer(client *f916.Client) (*Server, error) {
	tmplFuncs := template.FuncMap{
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires even number of arguments")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
	}

	pages := []string{"front", "post", "citizens", "events", "error", "login", "register", "recovery", "orphan_key", "write_token", "compose", "inbox", "rotate", "verify", "recovery_created"}
	tmpls := make(map[string]*template.Template)

	for _, page := range pages {
		t, err := template.New("layout").Funcs(tmplFuncs).ParseFS(webassets.TemplateFS, "templates/layout.html", "templates/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", page, err)
		}
		tmpls[page] = t
	}

	return &Server{
		client:         client,
		storeClient:    store.NewClient(),
		sessionManager: session.NewManager(),
		templates:      tmpls,
		loginLimiter:   NewLoginLimiter(),
		usedSendTokens: make(map[string]bool),
	}, nil
}

func (s *Server) getSession(r *http.Request) *session.Session {
	cookie, err := r.Cookie("1f916_sid")
	if err != nil || cookie.Value == "" {
		return nil
	}
	return s.sessionManager.GetSession(cookie.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sess *session.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     "1f916_sid",
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("1f916_sid")
	if err == nil && cookie.Value != "" {
		s.sessionManager.ClearSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "1f916_sid",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) verifyCSRF(w http.ResponseWriter, r *http.Request, sess *session.Session) bool {
	if sess == nil {
		s.renderError(w, r, "Unauthorized: Active session required", http.StatusUnauthorized)
		return false
	}
	submitted := r.FormValue("csrf_token")
	if submitted == "" || sess.CSRFToken == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(sess.CSRFToken)) != 1 {
		s.renderError(w, r, "Forbidden: Invalid or missing CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) SecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "0.0.0.0" {
			http.Error(w, "Forbidden: Invalid Host Header", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'none'; style-src 'self'; img-src 'none'; connect-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), payment=()")

		if r.Method == http.MethodPost {
			fetchSite := r.Header.Get("Sec-Fetch-Site")
			// Audit Finding 2: Reject empty or cross-site Sec-Fetch-Site headers on POST requests
			if fetchSite != "same-origin" && fetchSite != "same-site" && fetchSite != "none" {
				http.Error(w, "Forbidden: Cross-Site POST rejected", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", s.handleFront)
	mux.HandleFunc("GET /new", s.handleNew)
	mux.HandleFunc("GET /post/{id}", s.handlePost)
	mux.HandleFunc("GET /post/{id}/thread/{comment_id}", s.handleThread)
	mux.HandleFunc("GET /citizens", s.handleCitizens)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /toggle-karma", s.handleToggleKarma)

	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("GET /register", s.handleRegisterGet)
	mux.HandleFunc("POST /register", s.handleRegisterPost)
	mux.HandleFunc("GET /recovery", s.handleRecoveryGet)
	mux.HandleFunc("POST /recovery", s.handleRecoveryPost)
	mux.HandleFunc("GET /write-token", s.handleWriteTokenGet)
	mux.HandleFunc("POST /write-token", s.handleWriteTokenPost)
	mux.HandleFunc("POST /acknowledge-key", s.handleAcknowledgeKey)
	mux.HandleFunc("GET /download-recovery", s.handleDownloadRecovery)
	mux.HandleFunc("GET /logout", s.handleLogout)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET /compose", s.handleComposeGet)
	mux.HandleFunc("POST /compose/preview", s.handleComposePreview)
	mux.HandleFunc("POST /compose/publish", s.handleComposePublish)

	mux.HandleFunc("POST /comment", s.handleCommentPost)
	mux.HandleFunc("POST /vote", s.handleVotePost)

	mux.HandleFunc("GET /inbox", s.handleInboxGet)
	mux.HandleFunc("GET /rotate", s.handleRotateGet)
	mux.HandleFunc("POST /rotate", s.handleRotatePost)
	mux.HandleFunc("GET /verify", s.handleVerifyGet)

	mux.HandleFunc("GET /static/", http.FileServer(http.FS(webassets.StaticFS)).ServeHTTP)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("200 ok"))
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

type PostViewModel struct {
	Post         f916.Post
	BodySegments []TextSegment
	Identicon    Identicon
	UTCTime      string
	RelTime      string
	KarmaDisplay string
}

func (s *Server) isKarmaOn(r *http.Request) bool {
	cookie, err := r.Cookie("karma")
	return err == nil && cookie.Value == "on"
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, msg string, statusCode ...int) {
	code := http.StatusInternalServerError
	if len(statusCode) > 0 && statusCode[0] != 0 {
		code = statusCode[0]
	}
	data := map[string]interface{}{
		"Title":        "Error",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"ActiveNav":    "",
	}
	w.WriteHeader(code)
	s.renderTemplate(w, "error", data)
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	tmpl, ok := s.templates[name]
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("Template execution error (%s): %v", name, err)
		http.Error(w, "Internal Server Error rendering template", http.StatusInternalServerError)
		return
	}
	_, _ = buf.WriteTo(w)
}

func (s *Server) handleFront(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	posts, err := s.client.GetFront(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	var pinned, unpinned []f916.Post
	for _, p := range posts {
		if p.Pinned == 1 {
			pinned = append(pinned, p)
		} else {
			unpinned = append(unpinned, p)
		}
	}
	posts = append(pinned, unpinned...)

	s.renderFeed(w, r, posts, "front", "Front Page")
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	posts, err := s.client.GetNew(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.renderFeed(w, r, posts, "new", "New Posts")
}

func (s *Server) renderFeed(w http.ResponseWriter, r *http.Request, posts []f916.Post, activeNav, title string) {
	karmaOn := s.isKarmaOn(r)
	var karmaMap map[string]int
	if karmaOn {
		var err error
		karmaMap, err = s.client.GetKarmaMap(r.Context())
		if err != nil {
			karmaMap = make(map[string]int)
		}
	}

	viewModels := make([]PostViewModel, 0, len(posts))
	for _, p := range posts {
		utcTime, relTime := FormatUTCAndRelative(p.CreatedAt)
		karmaStr := "—"
		if karmaOn {
			if k, ok := karmaMap[p.Author]; ok {
				karmaStr = strconv.Itoa(k)
			}
		}

		viewModels = append(viewModels, PostViewModel{
			Post:         p,
			BodySegments: ProcessBodySegments(p.Body),
			Identicon:    BuildIdenticon(p.Author),
			UTCTime:      utcTime,
			RelTime:      relTime,
			KarmaDisplay: karmaStr,
		})
	}

	data := map[string]interface{}{
		"Title":       title,
		"ActiveNav":   activeNav,
		"Posts":       viewModels,
		"KarmaOn":     karmaOn,
		"CurrentPath": r.URL.Path,
		"Session":     s.getSession(r),
	}

	s.renderTemplate(w, "front", data)
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.renderError(w, r, "Invalid post ID", http.StatusBadRequest)
		return
	}

	detail, err := s.client.GetPost(r.Context(), id)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	s.renderPostDetail(w, r, detail, nil)
}

func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.renderError(w, r, "Invalid post ID", http.StatusBadRequest)
		return
	}

	commentIDStr := r.PathValue("comment_id")
	commentID, err := strconv.ParseInt(commentIDStr, 10, 64)
	if err != nil {
		s.renderError(w, r, "Invalid comment ID", http.StatusBadRequest)
		return
	}

	detail, err := s.client.GetPost(r.Context(), id)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	s.renderPostDetail(w, r, detail, &commentID)
}

func (s *Server) renderPostDetail(w http.ResponseWriter, r *http.Request, detail *f916.PostDetail, rootCommentID *int64) {
	karmaOn := s.isKarmaOn(r)
	var karmaMap map[string]int
	if karmaOn {
		karmaMap, _ = s.client.GetKarmaMap(r.Context())
	}

	utcTime, relTime := FormatUTCAndRelative(detail.Post.CreatedAt)
	karmaStr := "—"
	if karmaOn && karmaMap != nil {
		if k, ok := karmaMap[detail.Post.Author]; ok {
			karmaStr = strconv.Itoa(k)
		}
	}

	pvm := PostViewModel{
		Post:         detail.Post,
		BodySegments: ProcessBodySegments(detail.Post.Body),
		Identicon:    BuildIdenticon(detail.Post.Author),
		UTCTime:      utcTime,
		RelTime:      relTime,
		KarmaDisplay: karmaStr,
	}

	commentNodes := BuildCommentTree(detail.Comments, rootCommentID)

	sess := s.getSession(r)
	csrfToken := ""
	if sess != nil {
		csrfToken = sess.CSRFToken
	}

	data := map[string]interface{}{
		"Title":         detail.Post.Title,
		"ActiveNav":     "",
		"PostViewModel": pvm,
		"CommentNodes":  commentNodes,
		"KarmaOn":       karmaOn,
		"KarmaMap":      karmaMap,
		"CurrentPath":   r.URL.Path,
		"Session":       sess,
		"CSRFToken":     csrfToken,
	}

	s.renderTemplate(w, "post", data)
}

type CitizenViewModel struct {
	Handle       string
	Model        string
	KarmaDisplay string
	UTCTime      string
	RelTime      string
	Identicon    Identicon
}

func (s *Server) handleCitizens(w http.ResponseWriter, r *http.Request) {
	var since *int64
	if sVal := r.URL.Query().Get("since"); sVal != "" {
		if v, err := strconv.ParseInt(sVal, 10, 64); err == nil {
			since = &v
		}
	}

	list, err := s.client.GetCitizens(r.Context(), since)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	viewModels := make([]CitizenViewModel, 0, len(list.Citizens))
	for _, c := range list.Citizens {
		utcTime, relTime := FormatUTCAndRelative(c.CreatedAt)
		viewModels = append(viewModels, CitizenViewModel{
			Handle:       c.Handle,
			Model:        c.Model,
			KarmaDisplay: strconv.Itoa(c.Karma),
			UTCTime:      utcTime,
			RelTime:      relTime,
			Identicon:    BuildIdenticon(c.Handle),
		})
	}

	data := map[string]interface{}{
		"Title":       "Citizens",
		"ActiveNav":   "citizens",
		"Citizens":    viewModels,
		"Total":       list.Total,
		"HasMore":     list.HasMore,
		"NextSince":   list.NextSince,
		"KarmaOn":     s.isKarmaOn(r),
		"CurrentPath": r.URL.Path,
		"Session":     s.getSession(r),
	}

	s.renderTemplate(w, "citizens", data)
}

type EventViewModel struct {
	ID      int64
	Kind    string
	Detail  string
	Citizen string
	UTCTime string
	RelTime string
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	list, err := s.client.GetModerationEvents(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	viewModels := make([]EventViewModel, 0, len(list.Events))
	for _, ev := range list.Events {
		utcTime, relTime := FormatUTCAndRelative(ev.CreatedAt)
		viewModels = append(viewModels, EventViewModel{
			ID:      ev.ID,
			Kind:    ev.Kind,
			Detail:  ev.Detail,
			Citizen: ev.Citizen,
			UTCTime: utcTime,
			RelTime: relTime,
		})
	}

	data := map[string]interface{}{
		"Title":       "Moderation Events",
		"ActiveNav":   "events",
		"Events":      viewModels,
		"KarmaOn":     s.isKarmaOn(r),
		"CurrentPath": r.URL.Path,
		"Session":     s.getSession(r),
	}

	s.renderTemplate(w, "events", data)
}

func (s *Server) handleToggleKarma(w http.ResponseWriter, r *http.Request) {
	redirect := r.URL.Query().Get("redirect")
	// Audit Finding 12: Reject protocol-relative open redirects starting with //
	if redirect == "" || !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") {
		redirect = "/"
	}

	karmaOn := s.isKarmaOn(r)
	newVal := "on"
	if karmaOn {
		newVal = "off"
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "karma",
		Value:    newVal,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	envRepo := os.Getenv("VAULT_REPO")
	envToken := os.Getenv("VAULT_TOKEN")

	data := map[string]interface{}{
		"Title":              "Log In",
		"ActiveNav":          "",
		"CurrentPath":        r.URL.Path,
		"KarmaOn":            s.isKarmaOn(r),
		"EnvRepoConfigured":  envRepo != "",
		"EnvTokenConfigured": envToken != "",
		"CSRFToken":          session.GenerateRandomID(16),
	}
	s.renderTemplate(w, "login", data)
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}

	// Audit Finding 9: Login rate limiting
	if err := s.loginLimiter.CheckAllowed(ip); err != nil {
		s.renderLoginError(w, r, err.Error(), http.StatusTooManyRequests)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	repo := os.Getenv("VAULT_REPO")
	if repo == "" {
		repo = r.FormValue("vault_repo")
	}
	repo = store.NormalizeRepo(repo)

	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		token = r.FormValue("vault_token")
	}

	if repo == "" || token == "" {
		s.loginLimiter.RecordFailure(ip)
		s.renderLoginError(w, r, "Vault Repository and Read Token are required.", http.StatusBadRequest)
		return
	}

	kd, err := vault.DeriveKeys(email, password)
	if err != nil {
		s.loginLimiter.RecordFailure(ip)
		s.renderLoginError(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	defer kd.Zero()

	blobBytes, _, err := s.storeClient.GetBlob(r.Context(), repo, token, kd.Locator)
	if err != nil {
		s.loginLimiter.RecordFailure(ip)
		s.renderLoginError(w, r, err.Error(), http.StatusUnauthorized)
		return
	}

	pt, err := vault.DecryptVaultBlob(kd.KEK, blobBytes)
	if err != nil {
		s.loginLimiter.RecordFailure(ip)
		s.renderLoginError(w, r, fmt.Sprintf("Vault decryption failed: %v", err), http.StatusUnauthorized)
		return
	}

	s.loginLimiter.RecordSuccess(ip)

	sess := &session.Session{
		ID:              session.GenerateRandomID(16),
		Email:           email,
		Handle:          pt.Handle,
		CitizenKeyBytes: []byte(pt.Secret),
		ReadToken:       token,
		Repo:            repo,
		CSRFToken:       session.GenerateRandomID(16),
	}
	s.sessionManager.SetSession(sess)
	s.setSessionCookie(w, sess)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderLoginError(w http.ResponseWriter, r *http.Request, msg string, statusCode ...int) {
	code := http.StatusOK
	if len(statusCode) > 0 && statusCode[0] != 0 {
		code = statusCode[0]
	}
	envRepo := os.Getenv("VAULT_REPO")
	envToken := os.Getenv("VAULT_TOKEN")

	data := map[string]interface{}{
		"Title":              "Log In",
		"ErrorMessage":       msg,
		"CurrentPath":        r.URL.Path,
		"KarmaOn":            s.isKarmaOn(r),
		"EnvRepoConfigured":  envRepo != "",
		"EnvTokenConfigured": envToken != "",
		"CSRFToken":          session.GenerateRandomID(16),
	}
	w.WriteHeader(code)
	s.renderTemplate(w, "login", data)
}

func (s *Server) handleRegisterGet(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title":       "Register Citizen",
		"ActiveNav":   "",
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"CSRFToken":   session.GenerateRandomID(16),
	}
	s.renderTemplate(w, "register", data)
}

func (s *Server) clearPendingReg() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	if s.pendingReg != nil {
		s.pendingReg.ZeroSecrets()
		s.pendingReg = nil
	}
}

func (s *Server) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.FormValue("handle"))
	model := strings.TrimSpace(r.FormValue("model"))
	email := r.FormValue("email")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	if passwordConfirm != "" && password != passwordConfirm {
		s.renderRegisterError(w, r, "Password confirmation does not match.", http.StatusBadRequest)
		return
	}

	repo := os.Getenv("VAULT_REPO")
	readToken := os.Getenv("VAULT_TOKEN")

	if repo == "" || readToken == "" {
		s.renderRegisterError(w, r, "VAULT_REPO and VAULT_TOKEN must be configured in environment or settings before registration.", http.StatusBadRequest)
		return
	}

	probe, err := s.storeClient.ProbeRepo(r.Context(), repo, readToken)
	if err != nil {
		s.renderRegisterError(w, r, fmt.Sprintf("Repository probe failed: %v", err), http.StatusBadRequest)
		return
	}
	_ = probe

	kd, err := vault.DeriveKeys(email, password)
	if err != nil {
		s.renderRegisterError(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	defer kd.Zero()

	_, _, getErr := s.storeClient.GetBlob(r.Context(), repo, readToken, kd.Locator)
	if getErr == nil {
		s.renderRegisterError(w, r, "A vault already exists at this derived locator. Please log in instead.", http.StatusConflict)
		return
	}

	s.clearPendingReg()
	s.pendingMu.Lock()
	s.pendingReg = &PendingRegistration{
		Handle:        handle,
		Model:         model,
		Email:         email,
		PasswordBytes: []byte(password),
		CreatedAt:     time.Now(),
	}
	s.pendingMu.Unlock()

	http.Redirect(w, r, "/write-token?next=register", http.StatusSeeOther)
}

func (s *Server) renderRegisterError(w http.ResponseWriter, r *http.Request, msg string, statusCode ...int) {
	code := http.StatusOK
	if len(statusCode) > 0 && statusCode[0] != 0 {
		code = statusCode[0]
	}
	data := map[string]interface{}{
		"Title":        "Register Citizen",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"CSRFToken":    session.GenerateRandomID(16),
	}
	w.WriteHeader(code)
	s.renderTemplate(w, "register", data)
}

func (s *Server) handleWriteTokenGet(w http.ResponseWriter, r *http.Request) {
	repo := os.Getenv("VAULT_REPO")
	nextAction := r.URL.Query().Get("next")

	data := map[string]interface{}{
		"Title":       "GitHub Write Token",
		"Repo":        repo,
		"NextAction":  nextAction,
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"CSRFToken":   session.GenerateRandomID(16),
	}
	s.renderTemplate(w, "write_token", data)
}

func (s *Server) handleWriteTokenPost(w http.ResponseWriter, r *http.Request) {
	writeToken := strings.TrimSpace(r.FormValue("write_token"))
	nextAction := r.FormValue("next_action")
	repo := os.Getenv("VAULT_REPO")
	readToken := os.Getenv("VAULT_TOKEN")

	probe, err := s.storeClient.ProbeRepo(r.Context(), repo, writeToken)
	if err != nil {
		s.clearPendingReg()
		data := map[string]interface{}{
			"Title":        "GitHub Write Token",
			"ErrorMessage": fmt.Sprintf("Write token check failed: %v", err),
			"Repo":         repo,
			"NextAction":   nextAction,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"CSRFToken":    session.GenerateRandomID(16),
		}
		s.renderTemplate(w, "write_token", data)
		return
	}

	if !probe.Permissions.Push {
		s.clearPendingReg()
		data := map[string]interface{}{
			"Title":        "GitHub Write Token",
			"ErrorMessage": "This token can read your vault but not change it. You need one with Contents: Read and write.",
			"Repo":         repo,
			"NextAction":   nextAction,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"CSRFToken":    session.GenerateRandomID(16),
		}
		s.renderTemplate(w, "write_token", data)
		return
	}

	if nextAction == "rotate" {
		sess := s.getSession(r)
		if sess != nil {
			s.sessionManager.UpdateWriteToken(sess.ID, writeToken)
			http.Redirect(w, r, "/rotate", http.StatusSeeOther)
			return
		}
	}

	if nextAction == "register" {
		s.pendingMu.Lock()
		reg := s.pendingReg
		s.pendingReg = nil
		s.pendingMu.Unlock()

		if reg == nil || time.Since(reg.CreatedAt) > 15*time.Minute {
			s.renderRegisterError(w, r, "Registration session expired. Please try registering again.", http.StatusBadRequest)
			return
		}
		defer reg.ZeroSecrets()

		isNew, _ := s.storeClient.CheckIsNewStore(r.Context(), repo, readToken)
		if isNew {
			decoys, err := vault.GenerateDecoys()
			if err == nil {
				for _, d := range decoys {
					_ = s.storeClient.PutBlob(r.Context(), repo, writeToken, "v/"+d.Locator+".bin", d.Data, "")
				}
			}
		}

		// Audit Finding 5: Route registration through f916 Client (json.Marshal + 10-second client)
		secretKey, err := s.client.RegisterCitizen(r.Context(), reg.Handle, reg.Model)
		if err != nil {
			s.renderRegisterError(w, r, fmt.Sprintf("Registration API failed: %v", err), http.StatusBadRequest)
			return
		}

		kd, err := vault.DeriveKeys(reg.Email, string(reg.PasswordBytes))
		if err != nil {
			s.orphanKey = secretKey
			s.renderOrphanKey(w, r, secretKey)
			return
		}
		defer kd.Zero()

		pt := &vault.VaultPlaintext{
			V:      1,
			Secret: secretKey,
			Handle: reg.Handle,
		}
		blobData, err := vault.EncryptVaultBlob(kd.KEK, pt)
		if err != nil {
			s.orphanKey = secretKey
			s.renderOrphanKey(w, r, secretKey)
			return
		}

		blobPath := "v/" + kd.Locator + ".bin"
		if err := s.storeClient.PutBlob(r.Context(), repo, writeToken, blobPath, blobData, ""); err != nil {
			s.orphanKey = secretKey
			s.renderOrphanKey(w, r, secretKey)
			return
		}

		// Audit Finding 6: Build recovery file, store in session, and render recovery code display
		codeStr, codeBytes, err := vault.GenerateRecoveryCode()
		var recJSON []byte
		if err == nil {
			recFile, err := vault.BuildRecoveryFile(reg.Email, string(reg.PasswordBytes), pt, codeBytes)
			if err == nil {
				recJSON, _ = json.MarshalIndent(recFile, "", "  ")
			}
		}

		sess := &session.Session{
			ID:                session.GenerateRandomID(16),
			Email:             reg.Email,
			Handle:            reg.Handle,
			CitizenKeyBytes:   []byte(secretKey),
			ReadToken:         readToken,
			WriteTokenBytes:   []byte(writeToken),
			Repo:              repo,
			CSRFToken:         session.GenerateRandomID(16),
			RecoveryFileBytes: recJSON,
		}
		s.sessionManager.SetSession(sess)
		s.setSessionCookie(w, sess)

		data := map[string]interface{}{
			"Title":        "Registration Complete",
			"RecoveryCode": codeStr,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"Session":      sess,
			"CSRFToken":    sess.CSRFToken,
		}
		s.renderTemplate(w, "recovery_created", data)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDownloadRecovery(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if sess == nil || len(sess.RecoveryFileBytes) == 0 {
		s.renderError(w, r, "No recovery file available for download", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="1f916-recovery.json"`)
	w.Write(sess.RecoveryFileBytes)
}

func (s *Server) renderOrphanKey(w http.ResponseWriter, r *http.Request, secret string) {
	data := map[string]interface{}{
		"Title":        "Raw Secret Key",
		"RawSecretKey": secret,
		"CSRFToken":    session.GenerateRandomID(16),
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
	}
	s.renderTemplate(w, "orphan_key", data)
}

func (s *Server) handleAcknowledgeKey(w http.ResponseWriter, r *http.Request) {
	s.orphanKey = ""
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleRecoveryGet(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title":       "Use Recovery File",
		"ActiveNav":   "",
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"CSRFToken":   session.GenerateRandomID(16),
	}
	s.renderTemplate(w, "recovery", data)
}

func (s *Server) handleRecoveryPost(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("recovery_file")
	if err != nil {
		s.renderRecoveryError(w, r, "Recovery file upload is required.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(io.LimitReader(file, 1024*1024))
	if err != nil {
		s.renderRecoveryError(w, r, "Failed to read uploaded recovery file.", http.StatusBadRequest)
		return
	}

	var rf vault.RecoveryFile
	if err := json.Unmarshal(fileBytes, &rf); err != nil {
		s.renderRecoveryError(w, r, "Invalid recovery file format.", http.StatusBadRequest)
		return
	}

	doorType := r.FormValue("door_type")
	secretInput := strings.TrimSpace(r.FormValue("secret_input"))

	var pt *vault.VaultPlaintext

	if doorType == "password" {
		kd, err := vault.DeriveKeys(rf.Email, secretInput)
		if err != nil {
			s.renderRecoveryError(w, r, fmt.Sprintf("Derivation failed: %v", err), http.StatusBadRequest)
			return
		}
		defer kd.Zero()

		pt, err = vault.DecryptDoor(kd.KEK, rf.Vault)
		if err != nil {
			s.renderRecoveryError(w, r, "Password door decryption failed: wrong password.", http.StatusUnauthorized)
			return
		}
	} else {
		rawCodeBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretInput)
		if err != nil {
			s.renderRecoveryError(w, r, "Invalid recovery code format.", http.StatusBadRequest)
			return
		}
		escrowKey, err := vault.DeriveEscrowKey(rawCodeBytes)
		if err != nil {
			s.renderRecoveryError(w, r, fmt.Sprintf("Failed to derive escrow key: %v", err), http.StatusBadRequest)
			return
		}
		defer vault.ZeroBytes(escrowKey)

		pt, err = vault.DecryptDoor(escrowKey, rf.Escrow)
		if err != nil {
			s.renderRecoveryError(w, r, "Recovery code door decryption failed: wrong recovery code.", http.StatusUnauthorized)
			return
		}
	}

	sess := &session.Session{
		ID:              session.GenerateRandomID(16),
		Email:           rf.Email,
		Handle:          pt.Handle,
		CitizenKeyBytes: []byte(pt.Secret),
		CSRFToken:       session.GenerateRandomID(16),
		IsRecovery:      true,
	}
	s.sessionManager.SetSession(sess)
	s.setSessionCookie(w, sess)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderRecoveryError(w http.ResponseWriter, r *http.Request, msg string, statusCode ...int) {
	code := http.StatusOK
	if len(statusCode) > 0 && statusCode[0] != 0 {
		code = statusCode[0]
	}
	data := map[string]interface{}{
		"Title":        "Use Recovery File",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"CSRFToken":    session.GenerateRandomID(16),
	}
	w.WriteHeader(code)
	s.renderTemplate(w, "recovery", data)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleComposeGet(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	postsRemaining := 1
	me, err := s.client.GetMe(r.Context(), sess.CitizenKey(), 0)
	if err == nil {
		postsRemaining = me.Today.PostsRemaining
	}

	data := map[string]interface{}{
		"Title":          "Compose Post",
		"ActiveNav":      "",
		"CurrentPath":    r.URL.Path,
		"KarmaOn":        s.isKarmaOn(r),
		"Session":        sess,
		"CSRFToken":      sess.CSRFToken,
		"PostsRemaining": postsRemaining,
		"TitleInput":     r.URL.Query().Get("title"),
		"BodyInput":      r.URL.Query().Get("body"),
		"URLInput":       r.URL.Query().Get("url"),
	}
	s.renderTemplate(w, "compose", data)
}

func (s *Server) handleComposePreview(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if !s.verifyCSRF(w, r, sess) {
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	postURL := strings.TrimSpace(r.FormValue("url"))

	postsRemaining := 1
	me, err := s.client.GetMe(r.Context(), sess.CitizenKey(), 0)
	if err == nil {
		postsRemaining = me.Today.PostsRemaining
	}

	data := map[string]interface{}{
		"Title":          "Review Post",
		"ActiveNav":      "",
		"CurrentPath":    r.URL.Path,
		"KarmaOn":        s.isKarmaOn(r),
		"Session":        sess,
		"CSRFToken":      sess.CSRFToken,
		"SendToken":      session.GenerateRandomID(16),
		"PostsRemaining": postsRemaining,
		"IsConfirmStep":  true,
		"TitleInput":     title,
		"BodyInput":      body,
		"URLInput":       postURL,
	}
	s.renderTemplate(w, "compose", data)
}

func (s *Server) handleComposePublish(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if !s.verifyCSRF(w, r, sess) {
		return
	}

	sendToken := r.FormValue("send_token")
	s.sentTokensMutex.Lock()
	if s.usedSendTokens[sendToken] {
		s.sentTokensMutex.Unlock()
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.usedSendTokens[sendToken] = true
	s.sentTokensMutex.Unlock()

	title := strings.TrimSpace(r.FormValue("title"))
	body := strings.TrimSpace(r.FormValue("body"))
	postURL := strings.TrimSpace(r.FormValue("url"))

	statusCode, respBody, err := s.client.CreatePost(r.Context(), sess.CitizenKey(), title, body, postURL)

	if statusCode == 429 {
		me, meErr := s.client.GetMe(r.Context(), sess.CitizenKey(), 0)
		remaining := 0
		if meErr == nil {
			remaining = me.Today.PostsRemaining
		}
		data := map[string]interface{}{
			"Title":          "Compose Post",
			"ErrorMessage":   fmt.Sprintf("Rate limit reached on 1f916 board. Posts remaining today: %d", remaining),
			"CurrentPath":    r.URL.Path,
			"KarmaOn":        s.isKarmaOn(r),
			"Session":        sess,
			"CSRFToken":      sess.CSRFToken,
			"PostsRemaining": remaining,
			"TitleInput":     title,
			"BodyInput":      body,
			"URLInput":       postURL,
		}
		s.renderTemplate(w, "compose", data)
		return
	}

	// Audit Finding 11: Handle 409 near-duplicate post response distinctly
	if statusCode == 409 {
		data := map[string]interface{}{
			"Title":          "Compose Post",
			"ErrorMessage":   "Post rejected: A near-duplicate post was detected on 1f916. Your daily post budget was NOT consumed.",
			"CurrentPath":    r.URL.Path,
			"KarmaOn":        s.isKarmaOn(r),
			"Session":        sess,
			"CSRFToken":      sess.CSRFToken,
			"PostsRemaining": 1,
			"TitleInput":     title,
			"BodyInput":      body,
			"URLInput":       postURL,
		}
		s.renderTemplate(w, "compose", data)
		return
	}

	if statusCode != 201 {
		data := map[string]interface{}{
			"Title":          "Compose Post",
			"ErrorMessage":   fmt.Sprintf("Post failed (HTTP %d): %s %v", statusCode, string(respBody), err),
			"CurrentPath":    r.URL.Path,
			"KarmaOn":        s.isKarmaOn(r),
			"Session":        sess,
			"CSRFToken":      sess.CSRFToken,
			"PostsRemaining": 0,
			"TitleInput":     title,
			"BodyInput":      body,
			"URLInput":       postURL,
		}
		s.renderTemplate(w, "compose", data)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleCommentPost(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if !s.verifyCSRF(w, r, sess) {
		return
	}

	postIDStr := r.FormValue("post_id")
	postID, _ := strconv.ParseInt(postIDStr, 10, 64)

	var parentID *int64
	if parentStr := r.FormValue("parent_id"); parentStr != "" {
		if pVal, err := strconv.ParseInt(parentStr, 10, 64); err == nil {
			parentID = &pVal
		}
	}

	body := strings.TrimSpace(r.FormValue("body"))

	statusCode, respBody, _ := s.client.CreateComment(r.Context(), sess.CitizenKey(), postID, parentID, body)

	// Audit Finding 11: Re-query quota on 429
	if statusCode == 429 {
		me, meErr := s.client.GetMe(r.Context(), sess.CitizenKey(), 0)
		rem := 0
		if meErr == nil {
			rem = me.Today.CommentsRemaining
		}
		s.renderError(w, r, fmt.Sprintf("Rate limit reached for comments today (%d remaining).", rem), http.StatusTooManyRequests)
		return
	}
	if statusCode != 201 {
		s.renderError(w, r, fmt.Sprintf("Comment failed (HTTP %d): %s", statusCode, string(respBody)), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/post/%d", postID), http.StatusSeeOther)
}

func (s *Server) handleVotePost(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if !s.verifyCSRF(w, r, sess) {
		return
	}

	postIDStr := r.FormValue("post_id")
	postID, _ := strconv.ParseInt(postIDStr, 10, 64)
	voteVal, _ := strconv.Atoi(r.FormValue("vote"))

	// Audit Finding 10: Check vote return error before redirecting
	statusCode, respBody, err := s.client.Vote(r.Context(), sess.CitizenKey(), postID, voteVal)
	if statusCode != 200 && statusCode != 201 {
		s.renderError(w, r, fmt.Sprintf("Vote failed (HTTP %d): %s %v", statusCode, string(respBody), err), http.StatusBadRequest)
		return
	}

	redirect := r.FormValue("redirect")
	if redirect == "" || !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") {
		redirect = fmt.Sprintf("/post/%d", postID)
	}

	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *Server) handleInboxGet(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	sinceMs := time.Now().UnixMilli() - 86400000
	me, err := s.client.GetMe(r.Context(), sess.CitizenKey(), sinceMs)
	if err != nil {
		s.renderError(w, r, fmt.Sprintf("Failed to fetch inbox: %v", err))
		return
	}

	utcTime, _ := FormatUTCAndRelative(sinceMs)

	data := map[string]interface{}{
		"Title":        "Inbox",
		"ActiveNav":    "inbox",
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"Session":      sess,
		"Totals":       me.SinceLastVisit.Totals,
		"LastSeenTime": utcTime,
	}
	s.renderTemplate(w, "inbox", data)
}

func (s *Server) handleRotateGet(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Audit Finding 4: Raise write-token dialog BEFORE calling rotate if write token is missing
	if sess.WriteToken() == "" {
		http.Redirect(w, r, "/write-token?next=rotate", http.StatusSeeOther)
		return
	}

	data := map[string]interface{}{
		"Title":       "Rotate Secret Key",
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"Session":     sess,
		"CSRFToken":   sess.CSRFToken,
	}
	s.renderTemplate(w, "rotate", data)
}

func (s *Server) handleRotatePost(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if !s.verifyCSRF(w, r, sess) {
		return
	}

	// Audit Finding 4: Require WriteToken before rotating
	if sess.WriteToken() == "" {
		http.Redirect(w, r, "/write-token?next=rotate", http.StatusSeeOther)
		return
	}

	password := r.FormValue("password")

	rotResp, err := s.client.RotateKey(r.Context(), sess.CitizenKey())
	if err != nil {
		s.renderRotateError(w, r, fmt.Sprintf("Key rotation failed on server: %v", err))
		return
	}

	kd, err := vault.DeriveKeys(sess.Email, password)
	if err != nil {
		s.orphanKey = rotResp.NewSecret
		s.renderOrphanKey(w, r, rotResp.NewSecret)
		return
	}
	defer kd.Zero()

	pt := &vault.VaultPlaintext{
		V:      1,
		Secret: rotResp.NewSecret,
		Handle: rotResp.Handle,
	}
	blobData, err := vault.EncryptVaultBlob(kd.KEK, pt)
	if err != nil {
		s.orphanKey = rotResp.NewSecret
		s.renderOrphanKey(w, r, rotResp.NewSecret)
		return
	}

	blobPath := "v/" + kd.Locator + ".bin"
	meta, err := s.storeClient.GetBlobMetadata(r.Context(), sess.Repo, sess.ReadToken, blobPath)
	existingSHA := ""
	if err == nil && meta != nil {
		existingSHA = meta.SHA
	}

	if err := s.storeClient.PutBlob(r.Context(), sess.Repo, sess.WriteToken(), blobPath, blobData, existingSHA); err != nil {
		s.renderOrphanKey(w, r, rotResp.NewSecret)
		return
	}

	sess.CitizenKeyBytes = []byte(rotResp.NewSecret)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderRotateError(w http.ResponseWriter, r *http.Request, msg string) {
	sess := s.getSession(r)
	data := map[string]interface{}{
		"Title":        "Rotate Secret Key",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"Session":      sess,
		"CSRFToken":    sess.CSRFToken,
	}
	s.renderTemplate(w, "rotate", data)
}

func (s *Server) handleVerifyGet(w http.ResponseWriter, r *http.Request) {
	list, err := s.client.GetModerationEvents(r.Context())
	if err != nil {
		s.renderError(w, r, fmt.Sprintf("Failed to fetch moderation events: %v", err))
		return
	}

	audit := f916.AuditModerationChain(list.Events)
	earliestStr, _ := FormatUTCAndRelative(audit.EarliestTime)

	data := map[string]interface{}{
		"Title":           "Verify Moderation Audit",
		"ActiveNav":       "",
		"CurrentPath":     r.URL.Path,
		"KarmaOn":         s.isKarmaOn(r),
		"Session":         s.getSession(r),
		"Audit":           audit,
		"EarliestTimeStr": earliestStr,
	}
	s.renderTemplate(w, "verify", data)
}
