// Package app é o monólito modular: cada arquivo é um módulo (auth, cadastros,
// faturas, pipeline, jobs, automação, notificações, dashboard) sobre o mesmo banco.
// Os motores puros (audit, automation, workflow, br, ai, ocr) ficam em pacotes próprios.
package app

import (
	"database/sql"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ivankoelho/pague.ia/internal/ai"
	"github.com/ivankoelho/pague.ia/internal/notify"
	"github.com/ivankoelho/pague.ia/internal/ocr"
	"github.com/ivankoelho/pague.ia/internal/storage"
)

type Config struct {
	Addr           string
	DatabaseURL    string
	DataDir        string
	CookieSecure   bool
	AdminEmail     string
	AdminPassword  string
	SeedDemo       bool
	DryRun         bool // integrações externas não enviam nada (padrão: ligado)
	AutoApproveMax float64
	MaxUploadMB    int64
	Workers        int

	GroqKey, GroqModel string

	IMAPAddr, IMAPUser, IMAPPass, IMAPMailbox string

	SMTPHost, SMTPPort, SMTPUser, SMTPPass, SMTPFrom string
	WebhookURL                                       string

	WhatcURL, WhatcToken, WhatcMessagePath, WhatcTicketPath string
	WhatcGroups                                             map[string]string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func ConfigFromEnv() Config {
	f, _ := strconv.ParseFloat(env("AUTO_APPROVE_MAX", "0"), 64)
	mb, _ := strconv.ParseInt(env("MAX_UPLOAD_MB", "20"), 10, 64)
	w, _ := strconv.Atoi(env("WORKERS", "2"))
	groups := map[string]string{}
	for _, kv := range strings.Split(os.Getenv("WHATC_GROUPS"), ",") { // financeiro=1203...@g.us,ti=...
		if k, v, ok := strings.Cut(kv, "="); ok {
			groups[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return Config{
		Addr:             env("ADDR", ":8080"),
		DatabaseURL:      env("DATABASE_URL", "postgres://pague:pague@localhost:5432/pague?sslmode=disable"),
		DataDir:          env("DATA_DIR", "./data"),
		CookieSecure:     env("COOKIE_SECURE", "false") == "true",
		AdminEmail:       env("ADMIN_EMAIL", "admin@pague.local"),
		AdminPassword:    os.Getenv("ADMIN_PASSWORD"),
		SeedDemo:         env("SEED_DEMO", "false") == "true",
		DryRun:           env("INTEGRATIONS_DRY_RUN", "true") != "false",
		AutoApproveMax:   f,
		MaxUploadMB:      mb,
		Workers:          w,
		GroqKey:          os.Getenv("GROQ_API_KEY"),
		GroqModel:        os.Getenv("GROQ_MODEL"),
		IMAPAddr:         os.Getenv("IMAP_ADDR"),
		IMAPUser:         os.Getenv("IMAP_USER"),
		IMAPPass:         os.Getenv("IMAP_PASSWORD"),
		IMAPMailbox:      env("IMAP_MAILBOX", "INBOX"),
		SMTPHost:         os.Getenv("SMTP_HOST"),
		SMTPPort:         env("SMTP_PORT", "587"),
		SMTPUser:         os.Getenv("SMTP_USER"),
		SMTPPass:         os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:         env("SMTP_FROM", "pague@localhost"),
		WebhookURL:       os.Getenv("WEBHOOK_URL"),
		WhatcURL:         strings.TrimRight(os.Getenv("WHATC_URL"), "/"),
		WhatcToken:       os.Getenv("WHATC_TOKEN"),
		WhatcMessagePath: env("WHATC_MESSAGE_PATH", "/api/messages"),
		WhatcTicketPath:  os.Getenv("WHATC_TICKET_PATH"),
		WhatcGroups:      groups,
	}
}

type App struct {
	DB        *sql.DB
	Cfg       Config
	Store     storage.Store
	OCR       ocr.Extractor
	AI        ai.Provider // nil = sem IA, extração só por regras
	Notifiers map[string]notify.Provider
	Log       *slog.Logger
	Now       func() time.Time
	limiter   *limiter
}

func New(d *sql.DB, cfg Config) *App {
	a := &App{
		DB: d, Cfg: cfg,
		Store: storage.Local{Dir: cfg.DataDir + "/files"},
		OCR:   ocr.Local{},
		Log:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		Now:   time.Now,
		Notifiers: map[string]notify.Provider{
			"in_app":  notify.InApp{},
			"email":   notify.Email{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass, From: cfg.SMTPFrom, DryRun: cfg.DryRun},
			"webhook": notify.Webhook{URL: cfg.WebhookURL, DryRun: cfg.DryRun},
			"whatc": notify.Whatc{BaseURL: cfg.WhatcURL, Token: cfg.WhatcToken, MessagePath: cfg.WhatcMessagePath,
				TicketPath: cfg.WhatcTicketPath, Groups: cfg.WhatcGroups, DryRun: cfg.DryRun},
		},
		limiter: newLimiter(10, time.Minute),
	}
	if cfg.GroqKey != "" {
		a.AI = ai.NewGroq(cfg.GroqKey, cfg.GroqModel)
	}
	return a
}

// today devolve a data corrente (UTC, meia-noite) no fuso de operação.
func (a *App) today() time.Time {
	n := a.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

var loc = func() *time.Location {
	l, err := time.LoadLocation(env("TZ", "America/Bahia"))
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()
