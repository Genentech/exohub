package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/adb-standalone/internal/config"
	"github.com/Genentech/exohub/go/adb-standalone/internal/db"
	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/server"
	"github.com/Genentech/exohub/go/adb-standalone/internal/storage"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the adb-standalone HTTP server",
	RunE:  runServe,
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if cfg.Surreal.Password == "root" || cfg.Surreal.Password == "<change-me>" || cfg.Surreal.Password == "" {
		log.Println("WARNING: SurrealDB password is a default/placeholder — change before deploying to any non-local environment")
	}

	ctx := context.Background()

	log.Printf("connecting to SurrealDB at %s (ns=%s db=%s)", cfg.Surreal.URL, cfg.Surreal.NS, cfg.Surreal.DB)
	dbConn, err := db.Connect(ctx, &cfg.Surreal)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}
	defer func() {
		if err := dbConn.Close(ctx); err != nil {
			log.Printf("db close: %v", err)
		}
	}()

	log.Println("applying schema…")
	if err := dbConn.InitSchema(ctx, cfg.Semantic.ModelPath, cfg.Semantic.Dimensions); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}
	log.Println("schema ready")

	var adapter storage.Adapter
	if !cfg.Storage.S3.IsManaged() || cfg.Storage.S3.AccessKeyID != "" {
		log.Printf("initialising S3 storage adapter (endpoint=%q bucket=%q)", cfg.Storage.S3.Endpoint, cfg.Storage.S3.Bucket)
		s3adapter, err := storage.NewS3Adapter(ctx, &cfg.Storage.S3)
		if err != nil {
			return fmt.Errorf("storage adapter: %w", err)
		}
		if cfg.Storage.S3.Bucket != "" {
			log.Printf("ensuring bucket %q exists…", cfg.Storage.S3.Bucket)
			if err := storage.EnsureBucket(ctx, s3adapter.Client(), cfg.Storage.S3.Bucket, cfg.Storage.S3.Region); err != nil {
				log.Printf("WARNING: bucket ensure failed: %v", err)
			}
		}
		adapter = s3adapter
	} else {
		log.Println("WARNING: no S3 config (endpoint=managed) — /files/{id} endpoint disabled")
	}

	var embedder embed.Embedder
	if cfg.Semantic.ModelPath != "" {
		log.Printf("loading semantic embedding model from %q (dims=%d)…", cfg.Semantic.ModelPath, cfg.Semantic.Dimensions)
		e, err := embed.NewOnnxEmbedder(cfg.Semantic.ModelPath, cfg.Semantic.Dimensions)
		if err != nil {
			log.Printf("WARNING: semantic embedder init failed (%v) — /semantic-search will return 404", err)
		} else {
			embedder = e
			defer e.Close()
			log.Printf("semantic embedder ready (dims=%d)", cfg.Semantic.Dimensions)
		}
	} else {
		log.Println("semantic.model_path not set — semantic search disabled (FTS-only mode)")
	}

	srv := server.NewWithSemantic(cfg.Server.Addr, dbConn, cfg.Server.BaseURL, &cfg.Storage.S3, adapter, cfg, embedder)
	return srv.Run()
}

// loadConfig resolves the config file path and loads it.
func loadConfig() (*config.Config, error) {
	path := cfgFile
	if path == "" {
		// Try config.yaml in the current directory.
		if _, err := os.Stat("config.yaml"); err == nil {
			path = "config.yaml"
		}
	}
	if path == "" {
		log.Println("WARNING: no config file found — using built-in defaults, do not use in production")
	}
	return config.Load(path)
}
