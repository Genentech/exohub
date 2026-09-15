package serve

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Genentech/exohub/go/exo/palette"
)

//go:embed static
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Server is the web browse HTTP server.
type Server struct {
	store      *SessionStore
	catalogURL string
	staticFS   http.Handler
	tokenFile  string // if set, skip auth and use this token for all connections
}

func runServe(addr, catalogURL string) error {
	store := NewSessionStore()
	defer store.Stop()

	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("failed to create static FS: %w", err)
	}

	s := &Server{
		store:      store,
		catalogURL: catalogURL,
		staticFS:   http.FileServer(http.FS(subFS)),
		tokenFile:  os.Getenv("EXO_TOKEN_FILE"),
	}

	mux := http.NewServeMux()

	// Serve static files through a handler that ensures the session cookie
	// is set on regular HTTP responses (not WebSocket upgrades, where
	// gorilla/websocket skips headers set on the ResponseWriter).
	// App routes
	app := http.NewServeMux()
	app.HandleFunc("/", s.handleStatic)
	app.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(Version))
	})
	app.HandleFunc("/ws", s.handleWebSocket)

	// Mount under base path if set, otherwise at root
	if BasePath != "" {
		mux.Handle(BasePath+"/", http.StripPrefix(BasePath, app))
		// Redirect /base to /base/
		mux.HandleFunc(BasePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, BasePath+"/", http.StatusMovedPermanently)
		})
	} else {
		mux.Handle("/", app)
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Graceful shutdown on SIGTERM/SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("exo serve listening on %s (catalog: %s)", addr, catalogURL)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleStatic serves static files and ensures the session cookie is set.
// For index.html, injects a <base> tag if BasePath is configured.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	s.store.GetOrCreate(w, r)

	// Serve index.html with base path injection
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		data, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		html := string(data)
		html = strings.ReplaceAll(html, "__EXOHUB_CATALOG_URL__", s.catalogURL)
		html = strings.ReplaceAll(html, "__EXOHUB_THEMES__", buildThemesJSON())
		if BasePath != "" {
			baseTag := fmt.Sprintf(`<base href="%s/">`, BasePath)
			html = strings.Replace(html, "<title>", baseTag+"\n  <title>", 1)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
		return
	}

	s.staticFS.ServeHTTP(w, r)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract optional artifact ID for deep linking
	initialDoc := r.URL.Query().Get("doc")

	// Extract optional search params (support both short and long names)
	searchQ := r.URL.Query().Get("q")
	if searchQ == "" {
		searchQ = r.URL.Query().Get("query")
	}
	searchSort := r.URL.Query().Get("sort")
	searchFields := r.URL.Query().Get("fields")
	searchSize := r.URL.Query().Get("size")
	searchLatest := r.URL.Query().Get("latest")
	searchSchema := r.URL.Query().Get("schema")
	searchProject := r.URL.Query().Get("project")
	searchVersion := r.URL.Query().Get("version")

	// Extract theme preferences
	theme := r.URL.Query().Get("theme")
	themeMode := r.URL.Query().Get("theme_mode")

	// Look up existing session from cookie. The cookie was set by handleStatic
	// when the browser loaded the page.
	sess := s.store.GetFromRequest(r)
	if sess == nil {
		// No session cookie — shouldn't happen if the page loaded first,
		// but handle gracefully by creating one via the upgrade response headers.
		sess = s.store.Create()
		upgradeHeaders := http.Header{}
		upgradeHeaders.Set("Set-Cookie", sessionCookie(sess.ID).String())
		conn, err := upgrader.Upgrade(w, r, upgradeHeaders)
		if err != nil {
			log.Printf("WebSocket upgrade failed: %v", err)
			return
		}
		s.runSession(conn, sess, initialDoc, searchQ, searchSort, searchFields, searchSize, searchLatest, searchSchema, searchProject, searchVersion, theme, themeMode)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	s.runSession(conn, sess, initialDoc, searchQ, searchSort, searchFields, searchSize, searchLatest, searchSchema, searchProject, searchVersion, theme, themeMode)
}

func (s *Server) runSession(conn *websocket.Conn, sess *Session, initialDoc, searchQ, searchSort, searchFields, searchSize, searchLatest, searchSchema, searchProject, searchVersion, theme, themeMode string) {
	if s.tokenFile != "" {
		// Token file provided — skip auth entirely, go straight to terminal
		_ = sendControlMessage(conn, controlMessage{
			Type: "auth_done",
		})
	} else if sess.Token == nil || !sess.Token.Valid() {
		// No token — run the auth flow
		if err := s.runAuthFlow(conn, sess); err != nil {
			log.Printf("Auth flow failed for session %s: %v", sess.ID, err)
			_ = sendControlMessage(conn, controlMessage{
				Type:  "error",
				Error: err.Error(),
			})
			conn.Close()
			return
		}
	} else {
		// Already authenticated — tell the browser to start the terminal
		_ = sendControlMessage(conn, controlMessage{
			Type:     "auth_done",
			Username: sess.Username,
		})
	}

	// Each connection gets its own temp directory (unique connID) so that
	// page refresh doesn't race with the old bridge's cleanup.
	connID := generateSessionID()

	// Serialize the token as JSON to pass via EXO_TOKEN env var (no file needed)
	var tokenJSON string
	if s.tokenFile != "" {
		// Read the server-wide token file content
		data, err := os.ReadFile(s.tokenFile)
		if err != nil {
			log.Printf("Failed to read token file: %v", err)
			_ = sendControlMessage(conn, controlMessage{
				Type:  "error",
				Error: "Failed to prepare authentication",
			})
			conn.Close()
			return
		}
		tokenJSON = string(data)
	} else {
		data, err := json.Marshal(sess.Token)
		if err != nil {
			log.Printf("Failed to serialize token: %v", err)
			_ = sendControlMessage(conn, controlMessage{
				Type:  "error",
				Error: "Failed to prepare authentication",
			})
			conn.Close()
			return
		}
		tokenJSON = string(data)
	}

	// Write pre-populated search params if provided via URL
	if searchQ != "" || searchSort != "" || searchFields != "" || searchSize != "" || searchSchema != "" || searchProject != "" || searchVersion != "" {
		writeSearchParams(connID, searchQ, searchSort, searchFields, searchSize, searchLatest, searchSchema, searchProject, searchVersion)
	}

	// Start the PTY bridge
	bridge, err := NewPTYBridge(conn, s.catalogURL, tokenJSON, connID, initialDoc, theme, themeMode)
	if err != nil {
		log.Printf("Failed to create PTY bridge: %v", err)
		CleanupConnectionFiles(connID)
		conn.Close()
		return
	}

	// Start with default 24x80 — the browser sends a resize immediately
	// after startTerminal() which the PTY bridge handles via wsToPTY.
	if err := bridge.Start(24, 80); err != nil {
		log.Printf("PTY bridge ended for session %s (conn %s): %v", sess.ID, connID, err)
	}
}

func buildThemesJSON() string {
	type colorPairJSON struct {
		Light string `json:"light"`
		Dark  string `json:"dark"`
	}
	type themeInfo struct {
		Name        string        `json:"name"`
		Description string        `json:"description"`
		Accent      colorPairJSON `json:"accent"`
		Success     colorPairJSON `json:"success"`
		Error       colorPairJSON `json:"error"`
		Label       colorPairJSON `json:"label"`
		Dim         colorPairJSON `json:"dim"`
		CodeBg      colorPairJSON `json:"codeBg"`
	}
	cpJSON := func(cp palette.ColorPair) colorPairJSON {
		return colorPairJSON{Light: cp.Light, Dark: cp.Dark}
	}
	var themes []themeInfo
	for _, name := range palette.ThemeNames() {
		p := palette.Get(name)
		themes = append(themes, themeInfo{
			Name:        p.Name,
			Description: p.Description,
			Accent:      cpJSON(p.Accent),
			Success:     cpJSON(p.Success),
			Error:       cpJSON(p.Error),
			Label:       cpJSON(p.Label),
			Dim:         cpJSON(p.Dim),
			CodeBg:      cpJSON(p.CodeBg),
		})
	}
	data, _ := json.Marshal(themes)
	return string(data)
}

// runAuthFlow performs the OIDC device flow over the WebSocket.
func (s *Server) runAuthFlow(conn *websocket.Conn, sess *Session) error {
	authURL, userCode, resultCh, errCh, err := StartDeviceFlow()
	if err != nil {
		return err
	}

	// Tell the browser to show auth UI
	if err := sendControlMessage(conn, controlMessage{
		Type: "auth_required",
		URL:  authURL,
		Code: userCode,
	}); err != nil {
		return fmt.Errorf("failed to send auth_required: %w", err)
	}

	// Wait for auth to complete
	select {
	case result := <-resultCh:
		sess.Token = result.Token
		sess.Username = result.Username
		return sendControlMessage(conn, controlMessage{
			Type:     "auth_done",
			Username: result.Username,
		})
	case err := <-errCh:
		return err
	}
}

// writeSearchParams writes a pre-populated search_params.json into the
// per-connection search dir so the TUI starts with the given search state.
func writeSearchParams(connID, q, sort, fields, size, latest, schema, project, version string) {
	searchDir := connectionSearchDir(connID)
	if err := os.MkdirAll(searchDir, 0700); err != nil {
		log.Printf("Failed to create search dir: %v", err)
		return
	}

	// Build the full Lucene query from components
	var parts []string
	if project != "" {
		parts = append(parts, fmt.Sprintf(`_extra.project_id:"%s"`, project))
	}
	if version != "" {
		parts = append(parts, fmt.Sprintf(`_extra.version:"%s"`, version))
	} else if latest == "true" {
		parts = append(parts, "_extra.latest:true")
	}
	if schema != "" {
		parts = append(parts, fmt.Sprintf(`_extra.$schema:"%s"`, schema))
	}
	if q != "" {
		parts = append(parts, q)
	}
	query := strings.Join(parts, " AND ")
	if query == "" {
		query = "*"
	}

	params := struct {
		Q      string `json:"q"`
		Fields string `json:"fields,omitempty"`
		Size   int    `json:"size,omitempty"`
		Sort   string `json:"sort,omitempty"`
	}{
		Q:      query,
		Fields: fields,
		Sort:   sort,
	}
	if size != "" {
		if n, err := strconv.Atoi(size); err == nil {
			params.Size = n
		}
	}

	data, err := json.Marshal(params)
	if err != nil {
		log.Printf("Failed to marshal search params: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(searchDir, "search_params.json"), data, 0644); err != nil {
		log.Printf("Failed to write search_params.json: %v", err)
	}
}
