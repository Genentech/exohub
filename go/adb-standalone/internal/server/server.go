package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	surrealdb "github.com/surrealdb/surrealdb.go"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/db"
	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/handler"
	"github.com/Genentech/exohub/go/adb-standalone/internal/storage"
)

// Server is the adb-standalone HTTP server.
type Server struct {
	addr     string
	router   chi.Router
	db       *db.DB
	cfg      *config.Config
	baseURL  string
	s3cfg    *config.S3Config
	adapter  storage.Adapter
	embedder embed.Embedder
}

// New creates a configured Server. dbConn may be nil (health endpoint still works).
func New(addr string, dbConn *db.DB) *Server {
	return NewWithBaseURL(addr, dbConn, "")
}

// NewWithBaseURL creates a configured Server with an explicit base URL used
// for absolute URL generation (e.g. /schemas url field).
// Use NewWithStorage to also enable file-serving and ingest endpoints.
func NewWithBaseURL(addr string, dbConn *db.DB, baseURL string) *Server {
	return NewWithStorage(addr, dbConn, baseURL, nil, nil, nil)
}

// NewWithStorage creates a Server with a pluggable S3 storage adapter for file
// serving and an optional config for write endpoints (POST /project/ingest).
// Pass nil for adapter/s3cfg to disable file serving; nil cfg to disable ingest.
// Pass nil embedder to disable semantic search (returns 404 on /semantic-search).
func NewWithStorage(addr string, dbConn *db.DB, baseURL string, s3cfg *config.S3Config, adapter storage.Adapter, cfg *config.Config) *Server {
	return NewWithSemantic(addr, dbConn, baseURL, s3cfg, adapter, cfg, nil)
}

// NewWithSemantic is like NewWithStorage but also wires an optional Embedder
// for the /semantic-search endpoint.
func NewWithSemantic(addr string, dbConn *db.DB, baseURL string, s3cfg *config.S3Config, adapter storage.Adapter, cfg *config.Config, embedder embed.Embedder) *Server {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	s := &Server{addr: addr, router: r, db: dbConn, baseURL: baseURL, s3cfg: s3cfg, adapter: adapter, cfg: cfg, embedder: embedder}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.router.Route("/v1/{tenant}", func(r chi.Router) {
		r.Get("/health", healthHandler)
		if s.db != nil {
			inner := s.db.Inner()
			r.Get("/search", handler.Search(inner))
			r.Get("/scroll/{scrollID}", handler.Scroll(inner))
			r.Get("/files/{fileID}/metadata", handler.Metadata(inner))
			if s.adapter != nil && s.s3cfg != nil {
				r.Get("/files/{fileID}", handler.FilesServe(inner, s.adapter, s.s3cfg))
			}
			r.Get("/projects/{projectID}/versions", handler.Versions(inner))
			r.Get("/schemas", handler.Schemas(inner, s.baseURL))
			r.Get("/jobs/{jobID}", handler.Jobs())
			if s.cfg != nil {
				r.Post("/project/ingest", handler.Ingest(inner, s.cfg, s.embedder))
			}
			// /semantic-search: enabled when embedder != nil, returns 404 otherwise.
			r.Get("/semantic-search", handler.SemanticSearch(inner, s.embedder))
			// /entities: always registered; returns 404 when entity not found.
			r.Get("/entities", handler.Entities(inner))
		}
	})

	// Root health (convenience, no tenant required).
	s.router.Get("/health", healthHandler)
	s.router.Get("/", healthHandler)
}

// ServeHTTP implements http.Handler, useful for testing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// scrollGCInterval is how often expired scroll sessions are purged.
const scrollGCInterval = 10 * time.Minute

// startScrollGC launches a background goroutine that periodically deletes
// expired scroll_session records. It stops when ctx is cancelled.
func startScrollGC(ctx context.Context, inner *surrealdb.DB) {
	go func() {
		ticker := time.NewTicker(scrollGCInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := surrealdb.Query[any](ctx, inner,
					`DELETE scroll_session WHERE expires_at < time::now()`, nil)
				if err != nil {
					log.Printf("scroll GC: %v", err)
				}
			}
		}
	}()
}

// Run starts the HTTP server and blocks until SIGINT/SIGTERM.
func (s *Server) Run() error {
	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start background scroll session GC.
	gcCtx, gcCancel := context.WithCancel(context.Background())
	defer gcCancel()
	if s.db != nil {
		startScrollGC(gcCtx, s.db.Inner())
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("adb-standalone listening on %s", s.addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("listen: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		log.Printf("received signal %s — shutting down", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Println("server stopped")
	return nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
