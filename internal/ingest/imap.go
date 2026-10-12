// Package ingest lê a caixa de faturas por IMAP e extrai anexos.
// Cliente IMAP mínimo (LOGIN, SELECT, UID SEARCH/FETCH/STORE) só com a stdlib:
// é tudo que a ingestão precisa. Microsoft Graph/Gmail API entram como outra fonte.
package ingest

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	conn net.Conn
	r    *bufio.Reader
	tag  int
}

func DialTLS(addr string) (*Client, error) {
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", addr, nil)
	if err != nil {
		return nil, err
	}
	return New(c)
}

// New usa uma conexão já aberta (TLS em produção, pipe nos testes).
func New(conn net.Conn) (*Client, error) {
	c := &Client{conn: conn, r: bufio.NewReader(conn)}
	conn.SetDeadline(time.Now().Add(2 * time.Minute))
	if _, err := c.r.ReadString('\n'); err != nil { // saudação do servidor
		return nil, err
	}
	return c, nil
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// cmd envia um comando e devolve as linhas não-tagueadas; literais {n} vêm anexados à linha.
func (c *Client) cmd(format string, args ...any) ([]string, error) {
	c.tag++
	tag := "a" + strconv.Itoa(c.tag)
	if _, err := fmt.Fprintf(c.conn, tag+" "+format+"\r\n", args...); err != nil {
		return nil, err
	}
	var lines []string
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		// literal: "... {123}\r\n" seguido de 123 bytes
		for {
			t := strings.TrimRight(line, "\r\n")
			i := strings.LastIndex(t, "{")
			if i < 0 || !strings.HasSuffix(t, "}") {
				break
			}
			n, err := strconv.Atoi(t[i+1 : len(t)-1])
			if err != nil {
				break
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(c.r, buf); err != nil {
				return nil, err
			}
			rest, err := c.r.ReadString('\n')
			if err != nil {
				return nil, err
			}
			line = t + "\n" + string(buf) + rest
		}
		if strings.HasPrefix(line, tag+" ") {
			st := strings.TrimSpace(line[len(tag)+1:])
			if !strings.HasPrefix(st, "OK") {
				return lines, fmt.Errorf("imap: %s", st)
			}
			return lines, nil
		}
		lines = append(lines, line)
	}
}

func (c *Client) Login(user, pass string) error {
	_, err := c.cmd("LOGIN %s %s", quote(user), quote(pass))
	return err
}

func (c *Client) Select(mbox string) error {
	_, err := c.cmd("SELECT %s", quote(mbox))
	return err
}

func (c *Client) SearchUnseen() ([]uint32, error) {
	lines, err := c.cmd("UID SEARCH UNSEEN")
	var uids []uint32
	for _, l := range lines {
		if f := strings.Fields(l); len(f) > 2 && f[1] == "SEARCH" {
			for _, s := range f[2:] {
				if n, err := strconv.ParseUint(s, 10, 32); err == nil {
					uids = append(uids, uint32(n))
				}
			}
		}
	}
	return uids, err
}

// FetchRaw devolve a mensagem RFC 822 completa sem marcá-la como lida.
func (c *Client) FetchRaw(uid uint32) ([]byte, error) {
	lines, err := c.cmd("UID FETCH %d BODY.PEEK[]", uid)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		if i := strings.Index(l, "}\n"); i >= 0 && strings.Contains(l[:i], "BODY[]") {
			body := l[i+2:]
			if j := strings.LastIndex(body, ")"); j >= 0 {
				body = body[:j]
			}
			return []byte(body), nil
		}
	}
	return nil, fmt.Errorf("imap: mensagem %d sem corpo", uid)
}

func (c *Client) MarkSeen(uid uint32) error {
	_, err := c.cmd(`UID STORE %d +FLAGS (\Seen)`, uid)
	return err
}

func (c *Client) Logout() { c.cmd("LOGOUT"); c.conn.Close() }

type Attachment struct {
	Filename, Mime string
	Data           []byte
}

type Mail struct {
	From, Subject, MessageID string
	Attachments              []Attachment
}

var dec = new(mime.WordDecoder)

// Parse lê a mensagem e devolve os anexos de interesse (PDF, XML, imagens).
func Parse(raw []byte) (*Mail, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	subj, _ := dec.DecodeHeader(msg.Header.Get("Subject"))
	m := &Mail{From: msg.Header.Get("From"), Subject: subj, MessageID: msg.Header.Get("Message-Id")}
	err = walk(msg.Header, msg.Body, m)
	return m, err
}

type header interface{ Get(string) string }

func walk(h header, body io.Reader, m *Mail) error {
	ct, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		ct = "text/plain"
	}
	if strings.HasPrefix(ct, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := walk(p.Header, p, m); err != nil {
				return err
			}
		}
	}
	name := params["name"]
	if _, dp, err := mime.ParseMediaType(h.Get("Content-Disposition")); err == nil && dp["filename"] != "" {
		name = dp["filename"]
	}
	if n, err := dec.DecodeHeader(name); err == nil {
		name = n
	}
	ext := strings.ToLower(filepath.Ext(name))
	wanted := ct == "application/pdf" || strings.Contains(ct, "xml") || strings.HasPrefix(ct, "image/") ||
		ext == ".pdf" || ext == ".xml"
	if !wanted || name == "" {
		return nil
	}
	switch strings.ToLower(h.Get("Content-Transfer-Encoding")) {
	case "base64":
		body = base64.NewDecoder(base64.StdEncoding, &skipNewlines{body})
	case "quoted-printable":
		body = quotedprintable.NewReader(body)
	}
	data, err := io.ReadAll(io.LimitReader(body, 25<<20))
	if err != nil {
		return err
	}
	if ext == ".pdf" {
		ct = "application/pdf"
	}
	m.Attachments = append(m.Attachments, Attachment{Filename: filepath.Base(name), Mime: ct, Data: data})
	return nil
}

type skipNewlines struct{ r io.Reader }

func (s *skipNewlines) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for _, b := range p[:n] {
		if b != '\r' && b != '\n' {
			p[j] = b
			j++
		}
	}
	return j, err
}
