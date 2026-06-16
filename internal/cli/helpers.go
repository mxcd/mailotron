package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mxcd/mailotron/internal/config"
	"github.com/mxcd/mailotron/internal/email"
	"github.com/mxcd/mailotron/internal/imapclient"
	"github.com/mxcd/mailotron/internal/render"
	"github.com/mxcd/mailotron/internal/store"
	cli "github.com/urfave/cli/v3"
)

func loadConfig(cmd *cli.Command) (*config.Config, error) {
	return config.Load(cmd.String("config"))
}

func resolveAccount(cmd *cli.Command, cfg *config.Config) (*config.Account, error) {
	return cfg.Resolve(cmd.String("account"))
}

// composeInput assembles a render.Input from flags, the config defaults, and
// the on-disk store. Template/signature may be given by stored name or by file
// path (for stateless use without a populated store).
func composeInput(cmd *cli.Command, cfg *config.Config) (render.Input, error) {
	var in render.Input

	if tf := cmd.String("template-file"); tf != "" {
		b, err := os.ReadFile(tf)
		if err != nil {
			return in, fmt.Errorf("read template-file: %w", err)
		}
		in.Frame = string(b)
	} else {
		name := cmd.String("template")
		if name == "" {
			name = cfg.Defaults.Template
		}
		if name == "" {
			return in, usageErr("no template specified and no defaults.template set")
		}
		st, err := store.New()
		if err != nil {
			return in, err
		}
		frame, err := st.ReadTemplate(name)
		if err != nil {
			return in, err
		}
		in.Frame = frame
	}

	if sf := cmd.String("signature-file"); sf != "" {
		b, err := os.ReadFile(sf)
		if err != nil {
			return in, fmt.Errorf("read signature-file: %w", err)
		}
		in.Signature = string(b)
	} else {
		name := cmd.String("signature")
		if name == "" {
			name = cfg.Defaults.Signature
		}
		if name != "" {
			st, err := store.New()
			if err != nil {
				return in, err
			}
			if st.HasSignature(name) {
				sig, err := st.ReadSignature(name)
				if err != nil {
					return in, err
				}
				in.Signature = sig.MJML
			} else if cmd.IsSet("signature") {
				return in, fmt.Errorf("signature %q not found", name)
			}
		}
	}

	body, err := readBody(cmd)
	if err != nil {
		return in, err
	}
	in.Body = body

	bf, err := render.ParseBodyFormat(cmd.String("body-format"))
	if err != nil {
		return in, usageErr("%v", err)
	}
	in.BodyFormat = bf
	in.Subject = cmd.String("subject")
	in.PreviewText = cmd.String("preview")

	align := cmd.String("align")
	if align != "" && align != render.AlignLeft && align != render.AlignCenter {
		return in, usageErr("invalid --align %q (want left|center)", align)
	}
	in.Align = align

	vars, err := parseVars(cmd.StringSlice("var"), cmd.String("vars-file"))
	if err != nil {
		return in, err
	}
	in.Vars = vars
	return in, nil
}

func readBody(cmd *cli.Command) (string, error) {
	if b := cmd.String("body"); b != "" {
		return b, nil
	}
	bf := cmd.String("body-file")
	switch bf {
	case "":
		return "", nil
	case "-":
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	default:
		data, err := os.ReadFile(bf)
		if err != nil {
			return "", fmt.Errorf("read body-file: %w", err)
		}
		return string(data), nil
	}
}

func parseVars(pairs []string, file string) (map[string]any, error) {
	vars := map[string]any{}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read vars-file: %w", err)
		}
		if err := json.Unmarshal(b, &vars); err != nil {
			return nil, fmt.Errorf("parse vars-file: %w", err)
		}
	}
	for _, p := range pairs {
		i := strings.IndexByte(p, '=')
		if i < 0 {
			return nil, usageErr("invalid --var %q (want key=value)", p)
		}
		vars[p[:i]] = p[i+1:]
	}
	return vars, nil
}

func readAttachments(paths []string) ([]email.Attachment, error) {
	var out []email.Attachment
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("attach %q: %w", p, err)
		}
		ct := mime.TypeByExtension(filepath.Ext(p))
		if ct == "" {
			ct = "application/octet-stream"
		}
		out = append(out, email.Attachment{Filename: filepath.Base(p), ContentType: ct, Data: data})
	}
	return out, nil
}

// parseEmbeds turns repeated cid=path values into inline image attachments
// referenced from HTML as cid:<cid>.
func parseEmbeds(pairs []string) ([]email.Attachment, error) {
	var out []email.Attachment
	for _, p := range pairs {
		i := strings.IndexByte(p, '=')
		if i < 0 {
			return nil, usageErr("invalid --embed %q (want cid=path)", p)
		}
		cid, path := p[:i], p[i+1:]
		if cid == "" || path == "" {
			return nil, usageErr("invalid --embed %q (want cid=path)", p)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("embed %q: %w", path, err)
		}
		ct := mime.TypeByExtension(filepath.Ext(path))
		if ct == "" {
			ct = "application/octet-stream"
		}
		out = append(out, email.Attachment{
			Filename:    filepath.Base(path),
			ContentType: ct,
			ContentID:   cid,
			Data:        data,
		})
	}
	return out, nil
}

func parseUID(arg string) (uint32, error) {
	if arg == "" {
		return 0, usageErr("message UID argument required")
	}
	n, err := strconv.ParseUint(arg, 10, 32)
	if err != nil {
		return 0, usageErr("invalid UID %q", arg)
	}
	return uint32(n), nil
}

func connectIMAP(cmd *cli.Command, cfg *config.Config) (*imapclient.Client, *config.Account, error) {
	acc, err := resolveAccount(cmd, cfg)
	if err != nil {
		return nil, nil, err
	}
	if acc.IMAP == nil {
		return nil, nil, fmt.Errorf("account %q has no imap configuration", acc.Name)
	}
	c, err := imapclient.Connect(acc.IMAP)
	if err != nil {
		return nil, nil, err
	}
	return c, acc, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
