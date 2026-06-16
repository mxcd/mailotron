package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// InterpolateEnv replaces ${VAR} references with the corresponding environment
// variable value. It returns the sorted, de-duplicated list of referenced
// variables that were not set, so callers can surface misconfiguration.
//
// Missing-variable detection ignores references inside YAML comments, so a
// commented-out example account does not break a config that is otherwise
// fully populated.
func InterpolateEnv(raw []byte) (out []byte, missing []string) {
	miss := map[string]struct{}{}
	for _, m := range envRef.FindAllSubmatch(stripComments(raw), -1) {
		name := string(m[1])
		if _, ok := os.LookupEnv(name); !ok {
			miss[name] = struct{}{}
		}
	}
	out = envRef.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := envRef.FindSubmatch(match)[1]
		if val, ok := os.LookupEnv(string(name)); ok {
			return []byte(val)
		}
		return nil
	})
	for k := range miss {
		missing = append(missing, k)
	}
	sort.Strings(missing)
	return out, missing
}

// stripComments blanks out YAML line comments so env-var detection only
// considers live values. It is comment-aware of single/double quoting and only
// treats '#' as a comment when at line start or preceded by whitespace.
func stripComments(raw []byte) []byte {
	lines := bytes.Split(raw, []byte("\n"))
	for i, line := range lines {
		lines[i] = stripLineComment(line)
	}
	return bytes.Join(lines, []byte("\n"))
}

func stripLineComment(line []byte) []byte {
	var inSingle, inDouble bool
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
				return line[:i]
			}
		}
	}
	return line
}

// Parse unmarshals raw YAML (after env interpolation) into a validated Config.
// Unset ${VAR} references are recorded in Config.MissingEnv rather than failing,
// so commands that do not need secrets still work.
func Parse(raw []byte) (*Config, error) {
	interpolated, missing := InterpolateEnv(raw)
	var cfg Config
	if err := yaml.Unmarshal(interpolated, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.MissingEnv = missing
	for name, acc := range cfg.Accounts {
		if acc != nil {
			acc.Name = name
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Load reads, interpolates, parses and validates config from path. An empty
// override falls back to the resolved default location.
func Load(override string) (*Config, error) {
	path, err := FilePath(override)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config not found at %s (run `mailotron config init`)", path)
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
