// Comando único: aplica migrations, cria o admin inicial, sobe workers,
// agendador e o servidor HTTP com o frontend embutido.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pagueia "github.com/ivankoelho/pague.ia"
	"github.com/ivankoelho/pague.ia/internal/app"
	"github.com/ivankoelho/pague.ia/internal/db"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := app.ConfigFromEnv()
	fail := func(msg string, err error) {
		slog.Error(msg, "err", err)
		os.Exit(1)
	}
	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fail("banco indisponível", err)
	}
	if err := db.Migrate(ctx, d); err != nil {
		fail("migrations", err)
	}
	a := app.New(d, cfg)
	if err := a.EnsureAdmin(ctx); err != nil {
		fail("admin inicial", err)
	}
	if cfg.SeedDemo {
		if err := a.SeedDemo(ctx); err != nil {
			fail("seed demo", err)
		}
	}
	a.Run(ctx)
	srv := &http.Server{Addr: cfg.Addr, Handler: a.Handler(pagueia.Web(), pagueia.OpenAPI),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	a.Log.Info("servidor no ar", "addr", cfg.Addr, "dry_run", cfg.DryRun, "ia", cfg.GroqKey != "", "imap", cfg.IMAPAddr != "")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fail("http", err)
	}
}
