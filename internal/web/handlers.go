package web

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/tompickens06-tech/1f916-client/internal/f916"
	webassets "github.com/tompickens06-tech/1f916-client/web"
)

type Server struct {
	client    *f916.Client
	templates map[string]*template.Template
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

	pages := []string{"front", "post", "citizens", "events", "error"}
	tmpls := make(map[string]*template.Template)

	for _, page := range pages {
		t, err := template.New("layout").Funcs(tmplFuncs).ParseFS(webassets.TemplateFS, "templates/layout.html", "templates/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", page, err)
		}
		tmpls[page] = t
	}

	return &Server{
		client:    client,
		templates: tmpls,
	}, nil
}

func (s *Server) SecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Host check to defeat DNS rebinding
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
	IdenticonSVG template.HTML // Safe: SVG generated server-side from pure handle hash, no user input, presentation attributes only
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
	w.WriteHeader(http.StatusOK) // Present honest error page with 200 per standing rule
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
