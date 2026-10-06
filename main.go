// Command hochhuus serves the house lending list.
//
//	hochhuus          serve the app on $PORT
//	hochhuus migrate  bring the database schema up to date, then exit
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Bermos/hochhuus-leiht/internal/auth"
	"github.com/Bermos/hochhuus-leiht/internal/media"
	"github.com/Bermos/hochhuus-leiht/internal/server"
	"github.com/Bermos/hochhuus-leiht/internal/store"
	"github.com/Bermos/hochhuus-leiht/web"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, log)
	case "migrate":
		err = migrate(ctx, log)
	default:
		err = errors.New("unknown command " + cmd + "; want serve or migrate")
	}
	if err != nil {
		log.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func openStore(ctx context.Context) (*store.Store, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	return store.Open(ctx, url)
}

func migrate(ctx context.Context, log *slog.Logger) error {
	db, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	log.Info("schema up to date")
	return nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	db, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	// Running it here too is harmless and makes local development one step;
	// on Kitchen the migrate task has already done it before traffic arrives.
	if err := db.Migrate(ctx); err != nil {
		return err
	}

	var pictures *media.Store
	if cfg, ok := media.ConfigFromEnv(); ok {
		if pictures, err = media.New(cfg); err != nil {
			return err
		}
	} else {
		log.Warn("S3_ENDPOINT or S3_BUCKET not set; picture upload is off")
	}

	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
		log.Warn("SESSION_SECRET not set; sessions end when the process restarts")
	}
	houseCode := os.Getenv("HOUSE_CODE")
	if houseCode == "" {
		log.Warn("HOUSE_CODE not set; the list is open to anyone who finds it")
	}
	secure := strings.HasPrefix(os.Getenv("KITCHEN_URL"), "https://") || os.Getenv("SECURE_COOKIES") == "true"
	a := auth.New(secret, houseCode, os.Getenv("ADMIN_CODE"), secure)

	srv := &server.Server{Store: db, Media: pictures, Auth: a, Static: web.Dist(), Log: log}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	hs := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hs.Shutdown(shutdown)
	}()
	log.Info("listening", "port", port, "pictures", pictures != nil, "open", a.Open())
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
