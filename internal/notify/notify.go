// Package notify entrega mensagens por canal. Integrações reais só disparam
// quando configuradas e com DryRun desligado; caso contrário a mensagem é
// marcada como "mock" (nada sai do sistema).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

type Message struct {
	ID              int64
	Channel, Target string
	Subject, Body   string
	RefEntity       string
	RefID           int64
}

// Provider entrega por um canal. ErrMock indica simulação (dry-run/sem configuração).
type Provider interface {
	Send(ctx context.Context, m Message) error
}

var ErrMock = errors.New("simulado (dry-run ou integração não configurada)")

var client = &http.Client{Timeout: 20 * time.Second}

func postJSON(ctx context.Context, url, token string, body any) ([]byte, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 300 {
		return raw, fmt.Errorf("HTTP %d: %.200s", res.StatusCode, raw)
	}
	return raw, nil
}

// Webhook envia o JSON da mensagem para uma URL (n8n, ERP, etc.).
type Webhook struct {
	URL    string
	DryRun bool
}

func (w Webhook) Send(ctx context.Context, m Message) error {
	url := w.URL
	if strings.HasPrefix(m.Target, "https://") || strings.HasPrefix(m.Target, "http://") {
		url = m.Target
	}
	if url == "" || w.DryRun {
		return ErrMock
	}
	_, err := postJSON(ctx, url, "", m)
	return err
}

// Email via SMTP com STARTTLS (net/smtp).
type Email struct {
	Host, Port, User, Pass, From string
	DryRun                       bool
}

func (e Email) Send(ctx context.Context, m Message) error {
	if e.Host == "" || e.DryRun || m.Target == "" {
		return ErrMock
	}
	msg := "From: " + e.From + "\r\nTo: " + m.Target + "\r\nSubject: " + m.Subject +
		"\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + m.Body
	var auth smtp.Auth
	if e.User != "" {
		auth = smtp.PlainAuth("", e.User, e.Pass, e.Host)
	}
	return smtp.SendMail(e.Host+":"+e.Port, auth, e.From, []string{m.Target}, []byte(msg))
}

// Whatc é o adaptador para o gateway de WhatsApp/ocorrências já usado pela empresa.
// Os caminhos são configuráveis porque o contrato da API do Whatc ainda será alinhado.
// Fluxo: se TicketPath estiver definido, abre a ocorrência e anexa o protocolo à mensagem.
type Whatc struct {
	BaseURL, Token          string
	MessagePath, TicketPath string
	Groups                  map[string]string // alvo lógico ("financeiro") → id do grupo/contato no Whatc
	DryRun                  bool
}

func (w Whatc) Send(ctx context.Context, m Message) error {
	if w.BaseURL == "" || w.DryRun {
		return ErrMock
	}
	to := m.Target
	if g, ok := w.Groups[to]; ok {
		to = g
	}
	body := m.Body
	if w.TicketPath != "" {
		raw, err := postJSON(ctx, w.BaseURL+w.TicketPath, w.Token, map[string]any{
			"title": m.Subject, "description": m.Body, "group": to,
			"external_ref": fmt.Sprintf("%s:%d", m.RefEntity, m.RefID),
		})
		if err != nil {
			return fmt.Errorf("ocorrência: %w", err)
		}
		var t struct {
			ID       any    `json:"id"`
			Protocol string `json:"protocol"`
		}
		json.Unmarshal(raw, &t)
		if t.Protocol == "" && t.ID != nil {
			t.Protocol = fmt.Sprint(t.ID)
		}
		if t.Protocol != "" {
			body += "\nOcorrência Whatc: " + t.Protocol
		}
	}
	_, err := postJSON(ctx, w.BaseURL+w.MessagePath, w.Token, map[string]any{"to": to, "text": body})
	return err
}

// InApp não envia nada: a notificação já está gravada e aparece na interface.
type InApp struct{}

func (InApp) Send(context.Context, Message) error { return nil }
