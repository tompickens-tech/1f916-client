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

	envRepo  string
	envToken string

	orphanKeys map[string]string
	orphanMu   sync.Mutex

	pendingRegs map[string]*PendingRegistration
	pendingMu   sync.Mutex

	loginLimiter *LoginLimiter

	sentTokensMutex sync.Mutex
	usedSendTokens  map[string]bool

	logger *log.Logger
}

type PendingRegistration struct {
	Handle        string
	Model         string
	Email         string
	PasswordBytes []byte
	Repo          string
	CreatedAt     time.Time
}

func (p *PendingRegistration) ZeroSecrets() {
	if len(p.PasswordBytes) > 0 {
		vault.ZeroBytes(p.PasswordBytes)
		p.PasswordBytes = nil
	}
}

func (s *Server) sweeper() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.pendingMu.Lock()
		for token, reg := range s.pendingRegs {
			if time.Since(reg.CreatedAt) > 15*time.Minute {
				reg.ZeroSecrets()
				delete(s.pendingRegs, token)
			}
		}
		s.pendingMu.Unlock()
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

	if unlockTime, ok := l.locked[ip]; ok {
		if time.Now().Before(unlockTime) {
			return fmt.Errorf("Account locked. Try again in %v", time.Until(unlockTime).Round(time.Second))
		}
		delete(l.locked, ip)
		l.attempts[ip] = 0
	}
	return nil
}

func (l *LoginLimiter) RecordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip]++
	if l.attempts[ip] >= 5 {
		l.locked[ip] = time.Now().Add(5 * time.Minute)
	}
}

func (l *LoginLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[ip] = 0
	delete(l.locked, ip)
}

func NewServer(client *f916.Client, envRepo, envToken string) (*Server, error) {
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

	pages := []string{"front", "post", "citizens", "events", "error", "login", "register", "recovery", "orphan_key", "write_token", "compose", "inbox", "rotate", "verify", "recovery_created", "citizen", "official"}
	tmpls := make(map[string]*template.Template)

	for _, page := range pages {
		t, err := template.New("layout").Funcs(tmplFuncs).ParseFS(webassets.TemplateFS, "templates/layout.html", "templates/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", page, err)
		}
		tmpls[page] = t
	}

	s := &Server{
		client:         client,
		storeClient:    store.NewClient(),
		sessionManager: session.NewManager(),
		templates:      tmpls,
		envRepo:        envRepo,
		envToken:       envToken,
		orphanKeys:     make(map[string]string),
		pendingRegs:    make(map[string]*PendingRegistration),
		loginLimiter:   NewLoginLimiter(),
		usedSendTokens: make(map[string]bool),
		logger:         log.New(os.Stderr, "[web] ", log.LstdFlags),
	}
	go s.sweeper()
	return s, nil
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

type SessionView struct {
	Handle    string
	Email     string
}

func (s *Server) getSessionView(sess *session.Session) *SessionView {
	if sess == nil {
		return nil
	}
	sess.Mu.Lock()
	defer sess.Mu.Unlock()
	return &SessionView{
		Handle: sess.Handle,
		Email:  sess.Email,
	}
}

func (s *Server) getCSRFToken(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie("1f916_csrf")
	if err == nil && cookie.Value != "" {
		return cookie.Value
	}
	token := session.GenerateRandomID(16)
	http.SetCookie(w, &http.Cookie{
		Name:     "1f916_csrf",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return token
}

func (s *Server) verifyCSRF(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie("1f916_csrf")
	if err != nil || cookie.Value == "" {
		s.renderError(w, r, "Forbidden: Missing CSRF cookie", http.StatusForbidden)
		return false
	}
	submitted := r.FormValue("csrf_token")
	if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(cookie.Value)) != 1 {
		s.renderError(w, r, "Forbidden: Invalid or missing CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) setRegCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "1f916_reg",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) getRegCookie(r *http.Request) string {
	cookie, err := r.Cookie("1f916_reg")
	if err == nil {
		return cookie.Value
	}
	return ""
}

func (s *Server) clearRegCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "1f916_reg",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) resolveRepo(sess *session.Session, pendingReg *PendingRegistration, formRepo string) (string, bool) {
	if formRepo != "" {
		return formRepo, true
	}
	if sess != nil && sess.Repo != "" {
		return sess.Repo, true
	}
	if pendingReg != nil && pendingReg.Repo != "" {
		return pendingReg.Repo, true
	}
	if s.envRepo != "" {
		return s.envRepo, true
	}
	return "", false
}

func (s *Server) resolveReadToken(sess *session.Session, formToken string) (string, bool) {
	if formToken != "" {
		return formToken, true
	}
	if sess != nil && sess.ReadToken() != "" {
		return sess.ReadToken(), true
	}
	if s.envToken != "" {
		return s.envToken, true
	}
	return "", false
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
	s.renderTemplate(w, "error", data, code)
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data interface{}, statusCode ...int) {
	tmpl, ok := s.templates[name]
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		s.logger.Printf("Template execution error (%s): %v", name, err)
		http.Error(w, "Internal Server Error rendering template", http.StatusInternalServerError)
		return
	}
	if len(statusCode) > 0 && statusCode[0] != 0 {
		w.WriteHeader(statusCode[0])
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
		"Session":     s.getSessionView(s.getSession(r)),
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

	data := map[string]interface{}{
		"Title":         detail.Post.Title,
		"ActiveNav":     "",
		"PostViewModel": pvm,
		"CommentNodes":  commentNodes,
		"KarmaOn":       karmaOn,
		"KarmaMap":      karmaMap,
		"CurrentPath":   r.URL.Path,
		"Session":       s.getSessionView(sess),
		"CSRFToken":     s.getCSRFToken(w, r),
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
		"Session":     s.getSessionView(s.getSession(r)),
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
		"Session":     s.getSessionView(s.getSession(r)),
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
		"CSRFToken": s.getCSRFToken(w, r),
	}
	s.renderTemplate(w, "login", data)
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if !s.verifyCSRF(w, r) {
		return
	}

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

	formRepo := r.FormValue("vault_repo")
	formToken := r.FormValue("vault_token")

	repo, okRepo := s.resolveRepo(nil, nil, formRepo)
	token, okToken := s.resolveReadToken(nil, formToken)

	repo = store.NormalizeRepo(repo)

	if !okRepo || !okToken {
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
		ReadTokenBytes:  []byte(token),
		Repo:            repo,
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
		"CSRFToken": s.getCSRFToken(w, r),
	}
	s.renderTemplate(w, "login", data, code)
}

func (s *Server) handleRegisterGet(w http.ResponseWriter, r *http.Request) {
	envRepo := os.Getenv("VAULT_REPO")
	envToken := os.Getenv("VAULT_TOKEN")

	data := map[string]interface{}{
		"Title":              "Register Citizen",
		"ActiveNav":          "",
		"CurrentPath":        r.URL.Path,
		"KarmaOn":            s.isKarmaOn(r),
		"EnvRepoConfigured":  envRepo != "",
		"EnvTokenConfigured": envToken != "",
		"CSRFToken":          s.getCSRFToken(w, r),
	}
	s.renderTemplate(w, "register", data)
}

func (s *Server) clearPendingReg(w http.ResponseWriter, r *http.Request) {
	regToken := s.getRegCookie(r)
	if regToken != "" {
		s.pendingMu.Lock()
		if reg, ok := s.pendingRegs[regToken]; ok {
			reg.ZeroSecrets()
			delete(s.pendingRegs, regToken)
		}
		s.pendingMu.Unlock()
	}
}

func (s *Server) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	if !s.verifyCSRF(w, r) {
		return
	}

	handle := strings.TrimSpace(r.FormValue("handle"))
	model := strings.TrimSpace(r.FormValue("model"))
	email := r.FormValue("email")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	if passwordConfirm != "" && password != passwordConfirm {
		s.renderRegisterError(w, r, "Password confirmation does not match.", http.StatusBadRequest)
		return
	}

	formRepo := r.FormValue("vault_repo")
	repo, okRepo := s.resolveRepo(nil, nil, formRepo)
	repo = store.NormalizeRepo(repo)

	if !okRepo {
		s.renderRegisterError(w, r, "Vault Repository must be provided to register.", http.StatusBadRequest)
		return
	}

	kd, err := vault.DeriveKeys(email, password)
	if err != nil {
		s.renderRegisterError(w, r, err.Error(), http.StatusBadRequest)
		return
	}
	defer kd.Zero()

	s.clearPendingReg(w, r)
	
	regToken := session.GenerateRandomID(16)
	s.setRegCookie(w, regToken)

	s.pendingMu.Lock()
	s.pendingRegs[regToken] = &PendingRegistration{
		Handle:        handle,
		Model:         model,
		Email:         email,
		PasswordBytes: []byte(password),
		Repo:          repo,
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
	envRepo := os.Getenv("VAULT_REPO")
	envToken := os.Getenv("VAULT_TOKEN")

	data := map[string]interface{}{
		"Title":              "Register Citizen",
		"ErrorMessage":       msg,
		"CurrentPath":        r.URL.Path,
		"KarmaOn":            s.isKarmaOn(r),
		"EnvRepoConfigured":  envRepo != "",
		"EnvTokenConfigured": envToken != "",
		"CSRFToken":          s.getCSRFToken(w, r),
	}
	s.renderTemplate(w, "register", data, code)
}

func (s *Server) handleWriteTokenGet(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	
	var reg *PendingRegistration
	regToken := s.getRegCookie(r)
	if regToken != "" {
		s.pendingMu.Lock()
		reg = s.pendingRegs[regToken]
		s.pendingMu.Unlock()
	}

	repo, _ := s.resolveRepo(sess, reg, "")
	nextAction := r.URL.Query().Get("next")

	data := map[string]interface{}{
		"Title":       "GitHub Write Token",
		"Repo":        repo,
		"NextAction":  nextAction,
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"CSRFToken": s.getCSRFToken(w, r),
	}
	s.renderTemplate(w, "write_token", data)
}

func (s *Server) handleWriteTokenPost(w http.ResponseWriter, r *http.Request) {
	if !s.verifyCSRF(w, r) {
		return
	}

	writeToken := strings.TrimSpace(r.FormValue("write_token"))
	nextAction := r.FormValue("next_action")
	
	sess := s.getSession(r)
	
	var reg *PendingRegistration
	regToken := s.getRegCookie(r)
	if regToken != "" {
		s.pendingMu.Lock()
		reg = s.pendingRegs[regToken]
		s.pendingMu.Unlock()
	}

	repo, _ := s.resolveRepo(sess, reg, "")

	probe, err := s.storeClient.ProbeRepo(r.Context(), repo, writeToken)
	if err != nil {
		data := map[string]interface{}{
			"Title":        "GitHub Write Token",
			"ErrorMessage": fmt.Sprintf("Write token check failed: %v", err),
			"Repo":         repo,
			"NextAction":   nextAction,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"CSRFToken": s.getCSRFToken(w, r),
		}
		s.renderTemplate(w, "write_token", data)
		return
	}

	if !probe.Permissions.Push {
		data := map[string]interface{}{
			"Title":        "GitHub Write Token",
			"ErrorMessage": "This token can read your vault but not change it. You need one with Contents: Read and write.",
			"Repo":         repo,
			"NextAction":   nextAction,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"CSRFToken": s.getCSRFToken(w, r),
		}
		s.renderTemplate(w, "write_token", data)
		return
	}

	if nextAction == "rotate" {
		if sess != nil {
			s.sessionManager.UpdateWriteToken(sess.ID, writeToken)
			http.Redirect(w, r, "/rotate", http.StatusSeeOther)
			return
		}
	}

	if nextAction == "register" {
		if reg != nil {
			s.pendingMu.Lock()
			delete(s.pendingRegs, regToken)
			s.pendingMu.Unlock()
			s.clearRegCookie(w)
		}

		if reg == nil || time.Since(reg.CreatedAt) > 15*time.Minute {
			s.renderRegisterError(w, r, "Registration session expired. Please try registering again.", http.StatusBadRequest)
			return
		}
		defer reg.ZeroSecrets()

		isNew, _ := s.storeClient.CheckIsNewStore(r.Context(), repo, writeToken)
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
			s.renderError(w, r, "Failed to encrypt blob", http.StatusInternalServerError)
			return
		}

		blobPath := "v/" + kd.Locator + ".bin"
		if err := s.storeClient.PutBlob(r.Context(), repo, writeToken, blobPath, blobData, ""); err != nil {
			s.renderError(w, r, "Failed to save vault", http.StatusInternalServerError)
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

		readToken, _ := s.resolveReadToken(nil, "")
		if readToken == "" {
			readToken = writeToken
		}

		sess := &session.Session{
			ID:                session.GenerateRandomID(16),
			Email:             reg.Email,
			Handle:            reg.Handle,
			CitizenKeyBytes:   []byte(secretKey),
			ReadTokenBytes:    []byte(readToken),
			WriteTokenBytes:   []byte(writeToken),
			Repo:              repo,
			RecoveryFileBytes: recJSON,
		}
		s.sessionManager.SetSession(sess)
		s.setSessionCookie(w, sess)

		data := map[string]interface{}{
			"Title":        "Registration Complete",
			"RecoveryCode": codeStr,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"Session":     s.getSessionView(sess),
			"CSRFToken": s.getCSRFToken(w, r),
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
	regToken := s.getRegCookie(r)
	if regToken == "" {
		regToken = session.GenerateRandomID(16)
		s.setRegCookie(w, regToken)
	}
	s.orphanMu.Lock()
	s.orphanKeys[regToken] = secret
	s.orphanMu.Unlock()

	data := map[string]interface{}{
		"Title":        "Raw Secret Key",
		"RawSecretKey": secret,
		"CSRFToken":    s.getCSRFToken(w, r),
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
	}
	s.renderTemplate(w, "orphan_key", data)
}

func (s *Server) handleAcknowledgeKey(w http.ResponseWriter, r *http.Request) {
	regToken := s.getRegCookie(r)
	if regToken != "" {
		s.orphanMu.Lock()
		delete(s.orphanKeys, regToken)
		s.orphanMu.Unlock()
		s.clearRegCookie(w)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleRecoveryGet(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title":       "Use Recovery File",
		"ActiveNav":   "",
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"CSRFToken": s.getCSRFToken(w, r),
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
		"CSRFToken": s.getCSRFToken(w, r),
	}
	s.renderTemplate(w, "recovery", data, code)
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
		"Session":     s.getSessionView(sess),
		"CSRFToken": s.getCSRFToken(w, r),
		"PostsRemaining": postsRemaining,
		"TitleInput":     r.URL.Query().Get("title"),
		"BodyInput":      r.URL.Query().Get("body"),
		"URLInput":       r.URL.Query().Get("url"),
	}
	s.renderTemplate(w, "compose", data)
}

func (s *Server) handleComposePreview(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.verifyCSRF(w, r) {
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
		"Session":     s.getSessionView(sess),
		"CSRFToken": s.getCSRFToken(w, r),
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
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.verifyCSRF(w, r) {
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
			"Session":     s.getSessionView(sess),
			"CSRFToken": s.getCSRFToken(w, r),
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
			"Session":     s.getSessionView(sess),
			"CSRFToken": s.getCSRFToken(w, r),
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
			"Session":     s.getSessionView(sess),
			"CSRFToken": s.getCSRFToken(w, r),
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
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.verifyCSRF(w, r) {
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
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.verifyCSRF(w, r) {
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
		"Session":     s.getSessionView(sess),
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
	if sess.Repo == "" || sess.WriteToken() == "" {
		http.Redirect(w, r, "/write-token?next=rotate", http.StatusSeeOther)
		return
	}

	data := map[string]interface{}{
		"Title":       "Rotate Secret Key",
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"Session":     s.getSessionView(sess),
		"CSRFToken": s.getCSRFToken(w, r),
	}
	s.renderTemplate(w, "rotate", data)
}

func (s *Server) handleRotatePost(w http.ResponseWriter, r *http.Request) {
	sess := s.getSession(r)
	if sess == nil || sess.CitizenKey() == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.verifyCSRF(w, r) {
		return
	}

	// Audit Finding 4: Require WriteToken before rotating
	if sess.Repo == "" || sess.WriteToken() == "" {
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
		s.renderOrphanKey(w, r, rotResp.NewSecret)
		return
	}

	blobPath := "v/" + kd.Locator + ".bin"
	readTok := sess.ReadToken()
	if readTok == "" {
		readTok = sess.WriteToken()
	}

	meta, err := s.storeClient.GetBlobMetadata(r.Context(), sess.Repo, readTok, blobPath)
	existingSHA := ""
	if err == nil && meta != nil {
		existingSHA = meta.SHA
	}

	putErr := s.storeClient.PutBlob(r.Context(), sess.Repo, sess.WriteToken(), blobPath, blobData, existingSHA)
	if putErr != nil {
		s.renderOrphanKey(w, r, rotResp.NewSecret)
		return
	}

	// Verify read-back (Finding 21)
	backBlobBytes, _, getErr := s.storeClient.GetBlob(r.Context(), sess.Repo, readTok, kd.Locator)
	if getErr != nil {
		s.renderOrphanKey(w, r, rotResp.NewSecret)
		return
	}

	backPt, err := vault.DecryptVaultBlob(kd.KEK, backBlobBytes)
	if err != nil || backPt.Secret != rotResp.NewSecret {
		s.renderOrphanKey(w, r, rotResp.NewSecret)
		return
	}

	sess.Mu.Lock()
	sess.CitizenKeyBytes = []byte(rotResp.NewSecret)
	sess.Mu.Unlock()

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderRotateError(w http.ResponseWriter, r *http.Request, msg string) {
	sess := s.getSession(r)
	data := map[string]interface{}{
		"Title":        "Rotate Secret Key",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"Session":     s.getSessionView(sess),
		"CSRFToken": s.getCSRFToken(w, r),
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
		"Session":     s.getSessionView(s.getSession(r)),
		"Audit":           audit,
		"EarliestTimeStr": earliestStr,
	}
	s.renderTemplate(w, "verify", data)
}
