// Package server is the HTTP API and the web app.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Bermos/hochhuus-leiht/internal/auth"
	"github.com/Bermos/hochhuus-leiht/internal/media"
	"github.com/Bermos/hochhuus-leiht/internal/store"
)

// The lists the original page offers; stored values stay in German so
// backups move freely between the online and the offline edition.
var (
	Categories = []string{"Werkzeug", "Haushalt & Reinigung", "Küche & Backen", "Freizeit & Feste", "Sonstiges"}
	Statuses   = []string{"Nach Absprache", "Verfügbar", "Ausgeliehen", "Pausiert"}
)

const (
	maxUpload = 15 << 20
	maxImport = 5 << 20
)

// Server serves the API and the built frontend.
type Server struct {
	Store  *store.Store
	Media  *media.Store // nil when no bucket is configured
	Auth   *auth.Auth
	Static fs.FS
	Log    *slog.Logger

	mu       sync.Mutex
	failures map[string][]time.Time
}

// Handler answers every route.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)

	mux.HandleFunc("GET /api/session", s.session)
	mux.HandleFunc("POST /api/session", s.login)
	mux.HandleFunc("DELETE /api/session", s.logout)

	mux.HandleFunc("GET /api/items", s.member(s.listItems))
	mux.HandleFunc("POST /api/items", s.member(s.createItem))
	mux.HandleFunc("PUT /api/items/{id}", s.member(s.updateItem))
	mux.HandleFunc("DELETE /api/items/{id}", s.member(s.deleteItem))
	mux.HandleFunc("GET /api/items/{id}/image", s.member(s.getImage))
	mux.HandleFunc("PUT /api/items/{id}/image", s.member(s.putImage))
	mux.HandleFunc("DELETE /api/items/{id}/image", s.member(s.deleteImage))
	mux.HandleFunc("POST /api/import", s.member(s.importBackup))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, "Eintrag fehlt.")
	})

	mux.Handle("/", s.static())
	return s.headers(s.sameOrigin(mux))
}

// itemJSON is an item as the frontend sees it.
type itemJSON struct {
	store.Item
	ImageURL string `json:"imageUrl,omitempty"`
	Mine     bool   `json:"mine"`
	Editable bool   `json:"editable"`
}

func (s *Server) present(r *http.Request, it store.Item) itemJSON {
	owner := s.Auth.PeekOwner(r)
	out := itemJSON{Item: it}
	out.Mine = owner != "" && it.OwnerID == owner
	out.Editable = out.Mine || s.Auth.Role(r) == auth.Admin
	if it.ImageKey != "" {
		// The key changes with every upload, so the URL may be cached forever.
		out.ImageURL = "/api/items/" + url.PathEscape(it.ID) + "/image?v=" + url.QueryEscape(keyVersion(it.ImageKey))
	}
	return out
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	role := s.Auth.Role(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"signedIn":   role >= auth.Member,
		"admin":      role == auth.Admin,
		"open":       s.Auth.Open(),
		"pictures":   s.Media != nil,
		"categories": Categories,
		"statuses":   Statuses,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.throttled(ip) {
		fail(w, http.StatusTooManyRequests, "Zu viele Versuche. Bitte später nochmals versuchen.")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "Ungültige Eingabe.")
		return
	}
	role, ok := s.Auth.Login(w, body.Code)
	if !ok {
		s.recordFailure(ip)
		fail(w, http.StatusForbidden, "Der Code stimmt nicht.")
		return
	}
	s.Auth.Owner(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"signedIn": true, "admin": role == auth.Admin})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.List(r.Context())
	if err != nil {
		s.internal(w, "listing items", err)
		return
	}
	out := make([]itemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, s.present(r, it))
	}
	writeJSON(w, http.StatusOK, out)
}

type itemInput struct {
	Title, Category, Description, Name, Floor, Contact, Conditions, Status string
}

// limits are the original page's, in characters.
var limits = map[string]int{"title": 100, "description": 1200, "name": 80, "floor": 80, "contact": 200, "conditions": 600}

func (in *itemInput) clean() (store.Item, bool) {
	it := store.Item{
		Title: strings.TrimSpace(in.Title), Category: in.Category, Description: strings.TrimSpace(in.Description),
		Name: strings.TrimSpace(in.Name), Floor: strings.TrimSpace(in.Floor), Contact: strings.TrimSpace(in.Contact),
		Conditions: strings.TrimSpace(in.Conditions), Status: in.Status,
	}
	if it.Category == "" {
		it.Category = Categories[len(Categories)-1]
	}
	if it.Status == "" {
		it.Status = Statuses[0]
	}
	fields := map[string]string{"title": it.Title, "description": it.Description, "name": it.Name,
		"floor": it.Floor, "contact": it.Contact, "conditions": it.Conditions}
	for k, v := range fields {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > limits[k] {
			return it, false
		}
	}
	if it.Title == "" || it.Name == "" || it.Contact == "" ||
		!slices.Contains(Categories, it.Category) || !slices.Contains(Statuses, it.Status) {
		return it, false
	}
	return it, true
}

func (s *Server) createItem(w http.ResponseWriter, r *http.Request) {
	var in itemInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Ungültige Eingabe.")
		return
	}
	it, ok := in.clean()
	if !ok {
		fail(w, http.StatusUnprocessableEntity, "Bitte Pflichtfelder und Textlängen prüfen.")
		return
	}
	it.ID = newID()
	it.OwnerID = s.Auth.Owner(w, r)
	created, err := s.Store.Create(r.Context(), it)
	if err != nil {
		s.internal(w, "creating an item", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.presentAs(r, created, it.OwnerID))
}

// presentAs is present for a request whose owner cookie was only just issued.
func (s *Server) presentAs(r *http.Request, it store.Item, owner string) itemJSON {
	out := s.present(r, it)
	out.Mine = it.OwnerID == owner
	out.Editable = out.Editable || out.Mine
	return out
}

// editable loads an item the request may change.
func (s *Server) editable(w http.ResponseWriter, r *http.Request) (store.Item, bool) {
	it, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "Eintrag nicht gefunden oder nicht dein Eintrag.")
		return it, false
	}
	if err != nil {
		s.internal(w, "loading an item", err)
		return it, false
	}
	if !s.present(r, it).Editable {
		fail(w, http.StatusForbidden, "Eintrag nicht gefunden oder nicht dein Eintrag.")
		return it, false
	}
	return it, true
}

func (s *Server) updateItem(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.editable(w, r)
	if !ok {
		return
	}
	var in itemInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "Ungültige Eingabe.")
		return
	}
	it, valid := in.clean()
	if !valid {
		fail(w, http.StatusUnprocessableEntity, "Bitte Pflichtfelder und Textlängen prüfen.")
		return
	}
	it.ID = cur.ID
	updated, err := s.Store.Update(r.Context(), it)
	if err != nil {
		s.internal(w, "updating an item", err)
		return
	}
	writeJSON(w, http.StatusOK, s.present(r, updated))
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.editable(w, r); !ok {
		return
	}
	gone, err := s.Store.Delete(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internal(w, "deleting an item", err)
		return
	}
	s.removeObject(gone.ImageKey)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	it, err := s.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil || it.ImageKey == "" || s.Media == nil {
		http.NotFound(w, r)
		return
	}
	body, size, err := s.Media.Get(r.Context(), it.ImageKey)
	if err != nil {
		if media.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		s.internal(w, "reading a picture", err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if r.URL.Query().Get("v") == keyVersion(it.ImageKey) {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	io.Copy(w, body)
}

func (s *Server) putImage(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		fail(w, http.StatusServiceUnavailable, "Bilder sind nicht eingerichtet.")
		return
	}
	it, ok := s.editable(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	file, _, err := r.FormFile("image")
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "Das Bild ist zu gross (maximal 15 MB).")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil || len(raw) > maxUpload {
		fail(w, http.StatusRequestEntityTooLarge, "Das Bild ist zu gross (maximal 15 MB).")
		return
	}
	jpg, err := media.Normalize(raw)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "Dieses Bildformat wird nicht unterstützt. Bitte JPEG, PNG oder WebP verwenden.")
		return
	}
	key := "items/" + it.ID + "/" + newID() + ".jpg"
	if err := s.Media.Put(r.Context(), key, jpg); err != nil {
		s.internal(w, "storing a picture", err)
		return
	}
	updated, err := s.Store.SetImage(r.Context(), it.ID, key)
	if err != nil {
		s.removeObject(key)
		s.internal(w, "recording a picture", err)
		return
	}
	s.removeObject(it.ImageKey)
	writeJSON(w, http.StatusOK, s.present(r, updated))
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	it, ok := s.editable(w, r)
	if !ok {
		return
	}
	updated, err := s.Store.SetImage(r.Context(), it.ID, "")
	if err != nil {
		s.internal(w, "removing a picture", err)
		return
	}
	s.removeObject(it.ImageKey)
	writeJSON(w, http.StatusOK, s.present(r, updated))
}

// importBackup reads a backup of the offline edition (or this one's export)
// and adds the listings it does not have yet.
func (s *Server) importBackup(w http.ResponseWriter, r *http.Request) {
	if s.Auth.Role(r) != auth.Admin {
		fail(w, http.StatusForbidden, "Anfrage abgelehnt.")
		return
	}
	var backup struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Items   []struct {
			ID string `json:"id"`
			itemInput
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxImport)).Decode(&backup); err != nil ||
		backup.Format != "unser-haus-leiht" || backup.Version != 1 || len(backup.Items) > 5000 {
		fail(w, http.StatusUnprocessableEntity, "Dies ist keine gültige Ausleihlisten-Sicherung.")
		return
	}
	items := make([]store.Item, 0, len(backup.Items))
	for _, raw := range backup.Items {
		it, ok := raw.clean()
		if !ok || raw.ID == "" || len(raw.ID) > 100 {
			fail(w, http.StatusUnprocessableEntity, "Die Sicherung enthält ungültige Angaben.")
			return
		}
		it.ID = raw.ID
		items = append(items, it)
	}
	added, err := s.Store.Import(r.Context(), items, s.Auth.Owner(w, r))
	if err != nil {
		s.internal(w, "importing a backup", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": added, "skipped": len(items) - added})
}

func (s *Server) removeObject(key string) {
	if key == "" || s.Media == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Media.Delete(ctx, key); err != nil {
		s.Log.Warn("removing a picture", "key", key, "err", err)
	}
}

// member refuses a request that has not entered the house code.
func (s *Server) member(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Auth.Role(r) < auth.Member {
			fail(w, http.StatusUnauthorized, "Bitte anmelden.")
			return
		}
		h(w, r)
	}
}

// sameOrigin refuses a state-changing request sent by another site.
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					fail(w, http.StatusForbidden, "Anfrage abgelehnt.")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		if !strings.HasPrefix(r.URL.Path, "/offline/") {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

// static serves the built frontend, with index.html for every unknown path.
func (s *Server) static() http.Handler {
	files := http.FileServerFS(s.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if st, err := fs.Stat(s.Static, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.Static, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}

func (s *Server) internal(w http.ResponseWriter, doing string, err error) {
	s.Log.Error(doing, "err", err)
	fail(w, http.StatusInternalServerError, "Speichern nicht möglich. Deine Eingaben bleiben erhalten. Bitte erneut versuchen.")
}

// throttled allows five wrong codes per address per ten minutes.
func (s *Server) throttled(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	recent := slices.DeleteFunc(s.failures[ip], func(t time.Time) bool { return t.Before(cutoff) })
	if s.failures == nil {
		s.failures = map[string][]time.Time{}
	}
	if len(recent) == 0 {
		delete(s.failures, ip)
	} else {
		s.failures[ip] = recent
	}
	return len(recent) >= 5
}

func (s *Server) recordFailure(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == nil {
		s.failures = map[string][]time.Time{}
	}
	s.failures[ip] = append(s.failures[ip], time.Now())
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// The last hop is the one the platform's gateway added; earlier ones
		// are whatever the client claimed.
		parts := strings.Split(fwd, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func keyVersion(key string) string {
	i := strings.LastIndex(key, "/")
	return strings.TrimSuffix(key[i+1:], ".jpg")
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
