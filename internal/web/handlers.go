package web

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

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
}

type PendingRegistration struct {
	Handle   string
	Model    string
	Email    string
	Password string
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

	pages := []string{"front", "post", "citizens", "events", "error", "login", "register", "recovery", "orphan_key", "write_token"}
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
	}, nil
}

func (s *Server) getSession(r *http.Request) *session.Session {
	return s.sessionManager.GetActiveSession()
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

		// Security Headers
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'none'; style-src 'self'; img-src 'none'; connect-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), payment=()")

		// CSRF & State-changing request check
		if r.Method == http.MethodPost {
			fetchSite := r.Header.Get("Sec-Fetch-Site")
			if fetchSite != "" && fetchSite != "same-origin" && fetchSite != "same-site" && fetchSite != "none" {
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
	mux.HandleFunc("GET /logout", s.handleLogout)
	mux.HandleFunc("POST /logout", s.handleLogout)

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
	IdenticonSVG template.HTML
	UTCTime      string
	RelTime      string
	KarmaDisplay string
}

func (s *Server) isKarmaOn(r *http.Request) bool {
	cookie, err := r.Cookie("karma")
	return err == nil && cookie.Value == "on"
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, msg string) {
	data := map[string]interface{}{
		"Title":        "Error",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"ActiveNav":    "",
	}
	w.WriteHeader(http.StatusOK)
	s.templates["error"].Execute(w, data)
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
			IdenticonSVG: template.HTML(GenerateIdenticonSVG(p.Author, 24)),
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
	}

	s.templates["front"].Execute(w, data)
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.renderError(w, r, "Invalid post ID")
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
		s.renderError(w, r, "Invalid post ID")
		return
	}

	commentIDStr := r.PathValue("comment_id")
	commentID, err := strconv.ParseInt(commentIDStr, 10, 64)
	if err != nil {
		s.renderError(w, r, "Invalid comment ID")
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
		IdenticonSVG: template.HTML(GenerateIdenticonSVG(detail.Post.Author, 40)),
		UTCTime:      utcTime,
		RelTime:      relTime,
		KarmaDisplay: karmaStr,
	}

	commentNodes := BuildCommentTree(detail.Comments, rootCommentID)

	data := map[string]interface{}{
		"Title":         detail.Post.Title,
		"ActiveNav":     "",
		"PostViewModel": pvm,
		"CommentNodes":  commentNodes,
		"KarmaOn":       karmaOn,
		"KarmaMap":      karmaMap,
		"CurrentPath":   r.URL.Path,
	}

	s.templates["post"].Execute(w, data)
}

type CitizenViewModel struct {
	Handle       string
	Model        string
	KarmaDisplay string
	UTCTime      string
	RelTime      string
	IdenticonSVG template.HTML
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
			IdenticonSVG: template.HTML(GenerateIdenticonSVG(c.Handle, 24)),
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
	}

	s.templates["citizens"].Execute(w, data)
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
	}

	s.templates["events"].Execute(w, data)
}

func (s *Server) handleToggleKarma(w http.ResponseWriter, r *http.Request) {
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" || !strings.HasPrefix(redirect, "/") {
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

// Stage v0.2 Handlers: Login, Registration, Recovery, Write Token

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
	s.templates["login"].Execute(w, data)
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
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
		s.renderLoginError(w, r, "Vault Repository and Read Token are required.")
		return
	}

	// Derive locator and KEK
	kd, err := vault.DeriveKeys(email, password)
	if err != nil {
		s.renderLoginError(w, r, err.Error())
		return
	}
	defer kd.Zero()

	// Fetch blob with 404 disambiguation
	blobBytes, _, err := s.storeClient.GetBlob(r.Context(), repo, token, kd.Locator)
	if err != nil {
		s.renderLoginError(w, r, err.Error())
		return
	}

	// Decrypt vault blob
	pt, err := vault.DecryptVaultBlob(kd.KEK, blobBytes)
	if err != nil {
		s.renderLoginError(w, r, fmt.Sprintf("Vault decryption failed: %v", err))
		return
	}

	// Store session in memory
	sess := &session.Session{
		ID:         session.GenerateRandomID(16),
		Email:      email,
		Handle:     pt.Handle,
		CitizenKey: pt.Secret,
		ReadToken:  token,
		Repo:       repo,
		CSRFToken:  session.GenerateRandomID(16),
	}
	s.sessionManager.SetSession(sess)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderLoginError(w http.ResponseWriter, r *http.Request, msg string) {
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
	s.templates["login"].Execute(w, data)
}

func (s *Server) handleRegisterGet(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title":       "Register Citizen",
		"ActiveNav":   "",
		"CurrentPath": r.URL.Path,
		"KarmaOn":     s.isKarmaOn(r),
		"CSRFToken":   session.GenerateRandomID(16),
	}
	s.templates["register"].Execute(w, data)
}

func (s *Server) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.FormValue("handle"))
	model := strings.TrimSpace(r.FormValue("model"))
	email := r.FormValue("email")
	password := r.FormValue("password")

	repo := os.Getenv("VAULT_REPO")
	readToken := os.Getenv("VAULT_TOKEN")

	if repo == "" || readToken == "" {
		s.renderRegisterError(w, r, "VAULT_REPO and VAULT_TOKEN must be configured in environment or settings before registration.")
		return
	}

	// 1. Probe repo BEFORE anything else
	probe, err := s.storeClient.ProbeRepo(r.Context(), repo, readToken)
	if err != nil {
		s.renderRegisterError(w, r, fmt.Sprintf("Repository probe failed: %v", err))
		return
	}

	// Check permissions.push if present
	_ = probe

	// 2. Derive seed, locator, KEK
	kd, err := vault.DeriveKeys(email, password)
	if err != nil {
		s.renderRegisterError(w, r, err.Error())
		return
	}
	defer kd.Zero()

	// 3. Check locator is free
	_, _, getErr := s.storeClient.GetBlob(r.Context(), repo, readToken, kd.Locator)
	if getErr == nil {
		s.renderRegisterError(w, r, "A vault already exists at this derived locator. Please log in instead.")
		return
	}

	// Save pending registration and redirect to write token dialog
	s.pendingReg = &PendingRegistration{
		Handle:   handle,
		Model:    model,
		Email:    email,
		Password: password,
	}

	http.Redirect(w, r, "/write-token?next=register", http.StatusSeeOther)
}

func (s *Server) renderRegisterError(w http.ResponseWriter, r *http.Request, msg string) {
	data := map[string]interface{}{
		"Title":        "Register Citizen",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"CSRFToken":    session.GenerateRandomID(16),
	}
	s.templates["register"].Execute(w, data)
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
	s.templates["write_token"].Execute(w, data)
}

func (s *Server) handleWriteTokenPost(w http.ResponseWriter, r *http.Request) {
	writeToken := strings.TrimSpace(r.FormValue("write_token"))
	nextAction := r.FormValue("next_action")
	repo := os.Getenv("VAULT_REPO")
	readToken := os.Getenv("VAULT_TOKEN")

	// Verify write token
	probe, err := s.storeClient.ProbeRepo(r.Context(), repo, writeToken)
	if err != nil {
		data := map[string]interface{}{
			"Title":        "GitHub Write Token",
			"ErrorMessage": fmt.Sprintf("Write token check failed: %v", err),
			"Repo":         repo,
			"NextAction":   nextAction,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"CSRFToken":    session.GenerateRandomID(16),
		}
		s.templates["write_token"].Execute(w, data)
		return
	}

	if !probe.Permissions.Push {
		// Non-push token error per brief
		data := map[string]interface{}{
			"Title":        "GitHub Write Token",
			"ErrorMessage": "This token can read your vault but not change it. You need one with Contents: Read and write.",
			"Repo":         repo,
			"NextAction":   nextAction,
			"CurrentPath":  r.URL.Path,
			"KarmaOn":      s.isKarmaOn(r),
			"CSRFToken":    session.GenerateRandomID(16),
		}
		s.templates["write_token"].Execute(w, data)
		return
	}

	if nextAction == "register" && s.pendingReg != nil {
		reg := s.pendingReg
		s.pendingReg = nil

		// Seed 16 decoys if store is new
		isNew, _ := s.storeClient.CheckIsNewStore(r.Context(), repo, readToken)
		if isNew {
			decoys, err := vault.GenerateDecoys()
			if err == nil {
				for _, d := range decoys {
					_ = s.storeClient.PutBlob(r.Context(), repo, writeToken, "v/"+d.Locator+".bin", d.Data, "")
				}
			}
		}

		// Call /api/register on 1f916.ai
		regBody := fmt.Sprintf(`{"handle":"%s","model":"%s"}`, reg.Handle, reg.Model)
		resp, err := http.Post("https://1f916.ai/api/register", "application/json", bytes.NewBufferString(regBody))
		if err != nil {
			s.renderRegisterError(w, r, fmt.Sprintf("Registration API failed: %v", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode == 409 {
			s.renderRegisterError(w, r, fmt.Sprintf("Handle '%s' is already taken on 1f916 board.", reg.Handle))
			return
		}

		if resp.StatusCode != 201 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			s.renderRegisterError(w, r, fmt.Sprintf("Registration returned HTTP %d: %s", resp.StatusCode, string(body)))
			return
		}

		var regResp struct {
			CitizenID int64  `json:"citizen_id"`
			Handle    string `json:"handle"`
			Secret    string `json:"secret"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
			s.renderRegisterError(w, r, "Failed to decode registration secret.")
			return
		}

		// Derive keys
		kd, err := vault.DeriveKeys(reg.Email, reg.Password)
		if err != nil {
			s.orphanKey = regResp.Secret
			s.renderOrphanKey(w, r, regResp.Secret)
			return
		}
		defer kd.Zero()

		// Encrypt vault blob
		pt := &vault.VaultPlaintext{
			V:      1,
			Secret: regResp.Secret,
			Handle: regResp.Handle,
		}
		blobData, err := vault.EncryptVaultBlob(kd.KEK, pt)
		if err != nil {
			s.orphanKey = regResp.Secret
			s.renderOrphanKey(w, r, regResp.Secret)
			return
		}

		// Upload vault blob
		blobPath := "v/" + kd.Locator + ".bin"
		if err := s.storeClient.PutBlob(r.Context(), repo, writeToken, blobPath, blobData, ""); err != nil {
			// Step 8 failed -> Render raw orphan key screen!
			s.orphanKey = regResp.Secret
			s.renderOrphanKey(w, r, regResp.Secret)
			return
		}

		// Build recovery file & trigger force-download
		codeStr, codeBytes, err := vault.GenerateRecoveryCode()
		if err == nil {
			recFile, err := vault.BuildRecoveryFile(reg.Email, reg.Password, pt, codeBytes)
			if err == nil {
				recJSON, _ := json.MarshalIndent(recFile, "", "  ")
				_ = codeStr

				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Disposition", `attachment; filename="1f916-recovery.json"`)
				w.Write(recJSON)
				return
			}
		}

		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderOrphanKey(w http.ResponseWriter, r *http.Request, secret string) {
	data := map[string]interface{}{
		"Title":        "Raw Secret Key",
		"RawSecretKey": secret,
		"CSRFToken":    session.GenerateRandomID(16),
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
	}
	s.templates["orphan_key"].Execute(w, data)
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
	s.templates["recovery"].Execute(w, data)
}

func (s *Server) handleRecoveryPost(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("recovery_file")
	if err != nil {
		s.renderRecoveryError(w, r, "Recovery file upload is required.")
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(io.LimitReader(file, 1024*1024))
	if err != nil {
		s.renderRecoveryError(w, r, "Failed to read uploaded recovery file.")
		return
	}

	var rf vault.RecoveryFile
	if err := json.Unmarshal(fileBytes, &rf); err != nil {
		s.renderRecoveryError(w, r, "Invalid recovery file format.")
		return
	}

	doorType := r.FormValue("door_type")
	secretInput := strings.TrimSpace(r.FormValue("secret_input"))

	var pt *vault.VaultPlaintext

	if doorType == "password" {
		kd, err := vault.DeriveKeys(rf.Email, secretInput)
		if err != nil {
			s.renderRecoveryError(w, r, fmt.Sprintf("Derivation failed: %v", err))
			return
		}
		defer kd.Zero()

		pt, err = vault.DecryptDoor(kd.KEK, rf.Vault)
		if err != nil {
			s.renderRecoveryError(w, r, "Password door decryption failed: wrong password.")
			return
		}
	} else {
		// Recovery Code door
		rawCodeBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretInput)
		if err != nil {
			s.renderRecoveryError(w, r, "Invalid recovery code format.")
			return
		}
		escrowKey, err := vault.DeriveEscrowKey(rawCodeBytes)
		if err != nil {
			s.renderRecoveryError(w, r, fmt.Sprintf("Failed to derive escrow key: %v", err))
			return
		}
		defer vault.ZeroBytes(escrowKey)

		pt, err = vault.DecryptDoor(escrowKey, rf.Escrow)
		if err != nil {
			s.renderRecoveryError(w, r, "Recovery code door decryption failed: wrong recovery code.")
			return
		}
	}

	// Recovery file session (read, post, comment, vote, flag enabled)
	sess := &session.Session{
		ID:         session.GenerateRandomID(16),
		Email:      rf.Email,
		Handle:     pt.Handle,
		CitizenKey: pt.Secret,
		CSRFToken:  session.GenerateRandomID(16),
		IsRecovery: true,
	}
	s.sessionManager.SetSession(sess)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderRecoveryError(w http.ResponseWriter, r *http.Request, msg string) {
	data := map[string]interface{}{
		"Title":        "Use Recovery File",
		"ErrorMessage": msg,
		"CurrentPath":  r.URL.Path,
		"KarmaOn":      s.isKarmaOn(r),
		"CSRFToken":    session.GenerateRandomID(16),
	}
	s.templates["recovery"].Execute(w, data)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessionManager.ClearSession()
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
