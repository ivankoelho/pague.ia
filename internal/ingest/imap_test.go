package ingest

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
)

const pdf = "%PDF-1.4 demo"

func sampleMail() string {
	b64 := base64.StdEncoding.EncodeToString([]byte(pdf))
	return "From: faturas@demo.local\r\nSubject: =?UTF-8?Q?Fatura_Outubro?=\r\nMessage-Id: <1@demo>\r\n" +
		"Content-Type: multipart/mixed; boundary=XX\r\n\r\n" +
		"--XX\r\nContent-Type: text/plain\r\n\r\nSegue fatura.\r\n" +
		"--XX\r\nContent-Type: application/octet-stream; name=\"fatura.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"fatura.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		b64[:8] + "\r\n" + b64[8:] + "\r\n--XX--\r\n"
}

func TestParse(t *testing.T) {
	m, err := Parse([]byte(sampleMail()))
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Fatura Outubro" || len(m.Attachments) != 1 {
		t.Fatalf("%+v", m)
	}
	a := m.Attachments[0]
	if a.Filename != "fatura.pdf" || a.Mime != "application/pdf" || string(a.Data) != pdf {
		t.Fatalf("anexo: %+v %q", a, a.Data)
	}
}

// fakeServer simula um servidor IMAP o suficiente para o fluxo de ingestão.
func fakeServer(t *testing.T, conn net.Conn) {
	r := bufio.NewReader(conn)
	fmt.Fprint(conn, "* OK ready\r\n")
	msg := sampleMail()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(line)
		tag, cmd := f[0], strings.Join(f[1:], " ")
		switch {
		case strings.HasPrefix(cmd, "UID SEARCH"):
			fmt.Fprint(conn, "* SEARCH 7\r\n")
		case strings.HasPrefix(cmd, "UID FETCH 7"):
			fmt.Fprintf(conn, "* 1 FETCH (UID 7 BODY[] {%d}\r\n%s)\r\n", len(msg), msg)
		case strings.HasPrefix(cmd, "LOGIN") && !strings.Contains(cmd, `"secret"`):
			fmt.Fprintf(conn, "%s NO bad credentials\r\n", tag)
			continue
		}
		fmt.Fprintf(conn, "%s OK done\r\n", tag)
		if strings.HasPrefix(cmd, "LOGOUT") {
			return
		}
	}
}

func TestClientFlow(t *testing.T) {
	a, b := net.Pipe()
	go fakeServer(t, b)
	c, err := New(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("u", "wrong"); err == nil {
		t.Fatal("login inválido deveria falhar")
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.Login("u", "secret"))
	must(c.Select("INBOX"))
	uids, err := c.SearchUnseen()
	must(err)
	if len(uids) != 1 || uids[0] != 7 {
		t.Fatalf("uids %v", uids)
	}
	raw, err := c.FetchRaw(7)
	must(err)
	m, err := Parse(raw)
	must(err)
	if len(m.Attachments) != 1 {
		t.Fatalf("%+v", m)
	}
	must(c.MarkSeen(7))
	c.Logout()
}
