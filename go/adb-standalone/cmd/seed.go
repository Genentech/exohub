package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/Genentech/exohub/go/adb-standalone/internal/db"
	"github.com/Genentech/exohub/go/adb-standalone/internal/embed"
	"github.com/Genentech/exohub/go/adb-standalone/internal/seed"
)

var seedCmd = &cobra.Command{
	Use:   "seed <ndjson-file>",
	Short: "Load documents from an NDJSON file into SurrealDB",
	Args:  cobra.ExactArgs(1),
	RunE:  runSeed,
}

func runSeed(_ *cobra.Command, args []string) error {
	ndjsonPath := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return err
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

	if err := dbConn.InitSchema(ctx, cfg.Semantic.ModelPath, cfg.Semantic.Dimensions); err != nil {
		return fmt.Errorf("schema init: %w", err)
	}

	f, err := os.Open(ndjsonPath)
	if err != nil {
		return fmt.Errorf("open %q: %w", ndjsonPath, err)
	}
	defer f.Close()

	var embedder embed.Embedder
	if cfg.Semantic.ModelPath != "" {
		e, err := embed.NewOnnxEmbedder(cfg.Semantic.ModelPath, cfg.Semantic.Dimensions)
		if err != nil {
			log.Printf("WARNING: semantic embedder init failed (%v) — seeding without embeddings", err)
		} else {
			embedder = e
			defer e.Close()
			log.Printf("semantic embedder ready (dims=%d)", cfg.Semantic.Dimensions)
		}
	}

	log.Printf("seeding from %s…", ndjsonPath)
	res, err := seed.Seed(ctx, dbConn.Inner(), f, os.Stdout, embedder)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	fmt.Printf("✓ loaded %d documents", res.Loaded)
	if res.Errors > 0 {
		fmt.Printf(" (%d errors)", res.Errors)
	}
	fmt.Println()
	return nil
}
