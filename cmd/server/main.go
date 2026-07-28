// Command server runs the whentorun.com HTTP server.
// Wiring only — routes live in internal/web.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	webserver "github.com/kuitang/whentorun/internal/web"
	webassets "github.com/kuitang/whentorun/web"
)

func main() {
	dev := flag.Bool("dev", false, "serve web assets from ./web on disk instead of the embedded copy (run from the repo root)")
	flag.Parse()

	var assets fs.FS = webassets.Files
	if *dev {
		assets = os.DirFS("web")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           webserver.NewServer(webserver.Options{Assets: assets, Dev: *dev}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("listening on :%s (dev=%v)", port, *dev)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	case <-ctx.Done():
		stop() // restore default signal handling: a second ^C kills immediately
		log.Printf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}
