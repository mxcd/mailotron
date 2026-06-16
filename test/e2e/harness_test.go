//go:build e2e

// Package e2e exercises the full mailotron CLI against real mail backends:
// GreenMail (SMTP + IMAP, for send→retrieve round-trips and folder/message
// management) and Mailpit (SMTP + HTTP, for send-side assertions).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcli "github.com/mxcd/mailotron/internal/cli"
	"github.com/mxcd/mailotron/internal/imapclient"
	"github.com/mxcd/mailotron/internal/store"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	gmHost  string
	gmSMTP  string
	gmIMAP  string
	pitHost string
	pitSMTP string
	pitHTTP string // host:port for Mailpit HTTP API
)

const (
	// GreenMail's `-Dgreenmail.users=agent:secret@mailotron.test` creates a user
	// whose IMAP/SMTP login is the part before ':' (agent) and whose email
	// address is agent@mailotron.test.
	imapLogin = "agent"
	imapUser  = "agent@mailotron.test" // recipient / From address
	imapPass  = "secret"
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	gm, err := startContainer(ctx, testcontainers.ContainerRequest{
		Image:        "greenmail/standalone:2.1.0",
		ExposedPorts: []string{"3025/tcp", "3143/tcp"},
		Env: map[string]string{
			"GREENMAIL_OPTS": "-Dgreenmail.setup.test.all -Dgreenmail.hostname=0.0.0.0 " +
				"-Dgreenmail.users=agent:secret@mailotron.test -Dgreenmail.verbose",
		},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("3025/tcp"),
			wait.ForListeningPort("3143/tcp"),
		).WithStartupTimeoutDefault(90 * time.Second),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start greenmail:", err)
		os.Exit(1)
	}

	pit, err := startContainer(ctx, testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:v1.21",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForHTTP("/").WithPort("8025/tcp").WithStartupTimeout(60 * time.Second),
	})
	if err != nil {
		_ = gm.Terminate(ctx)
		fmt.Fprintln(os.Stderr, "start mailpit:", err)
		os.Exit(1)
	}

	gmHost, _ = gm.Host(ctx)
	smtpP, _ := gm.MappedPort(ctx, "3025/tcp")
	imapP, _ := gm.MappedPort(ctx, "3143/tcp")
	gmSMTP, gmIMAP = smtpP.Port(), imapP.Port()

	pitHost, _ = pit.Host(ctx)
	psmtp, _ := pit.MappedPort(ctx, "1025/tcp")
	phttp, _ := pit.MappedPort(ctx, "8025/tcp")
	pitSMTP = psmtp.Port()
	pitHTTP = fmt.Sprintf("%s:%s", pitHost, phttp.Port())

	dir, _ := os.MkdirTemp("", "mailotron-e2e")
	os.Setenv("MAILOTRON_CONFIG_DIR", dir)
	if err := writeConfig(dir); err != nil {
		fmt.Fprintln(os.Stderr, "write config:", err)
		os.Exit(1)
	}
	st, _ := store.New()
	if _, err := st.Seed(true); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}

	code := m.Run()

	_ = gm.Terminate(ctx)
	_ = pit.Terminate(ctx)
	os.Exit(code)
}

func startContainer(ctx context.Context, req testcontainers.ContainerRequest) (testcontainers.Container, error) {
	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
}

func writeConfig(dir string) error {
	cfg := fmt.Sprintf(`defaults:
  account: green
  template: default
  signature: default
accounts:
  green:
    from: "Agent <%s>"
    outbound: { type: smtp, host: %s, port: %s, tls: none }
    imap: { host: %s, port: %s, tls: none, username: %s, password: %s }
  pit:
    from: "Agent <%s>"
    outbound: { type: smtp, host: %s, port: %s, tls: none }
`, imapUser, gmHost, gmSMTP, gmHost, gmIMAP, imapLogin, imapPass,
		imapUser, pitHost, pitSMTP)
	return os.WriteFile(filepath.Join(dir, "config.yml"), []byte(cfg), 0o600)
}

// ---- CLI invocation helpers --------------------------------------------

func runCLI(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	app := mcli.New("e2e", &out, &errb)
	full := append([]string{"mailotron"}, args...)
	err := app.Run(context.Background(), full)
	if code := mcli.HandleError(app, full, err); code != 0 {
		t.Fatalf("cmd %v failed (code %d): %s", args, code, errb.String())
	}
	return out.String()
}

func runJSON(t *testing.T, v any, args ...string) {
	t.Helper()
	out := runCLI(t, args...)
	if v != nil {
		if err := json.Unmarshal([]byte(out), v); err != nil {
			t.Fatalf("decode json for %v: %v\noutput: %s", args, err, out)
		}
	}
}

type listResult struct {
	Folder   string                      `json:"folder"`
	Count    int                         `json:"count"`
	Messages []imapclient.MessageSummary `json:"messages"`
}

// waitForSubject polls an account/folder until a message with the given subject
// appears, returning it.
func waitForSubject(t *testing.T, account, folder, subject string) imapclient.MessageSummary {
	t.Helper()
	for i := 0; i < 50; i++ {
		var res listResult
		runJSON(t, &res, "-o", "json", "-a", account, "message", "list", "--folder", folder, "--limit", "100")
		for _, m := range res.Messages {
			if m.Subject == subject {
				return m
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("message %q never arrived in %s/%s", subject, account, folder)
	return imapclient.MessageSummary{}
}

func mailpitFindSubject(t *testing.T, subject string) bool {
	t.Helper()
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://" + pitHTTP + "/api/v1/messages")
		if err == nil {
			var data struct {
				Messages []struct {
					Subject string `json:"Subject"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&data)
			resp.Body.Close()
			for _, m := range data.Messages {
				if m.Subject == subject {
					return true
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
