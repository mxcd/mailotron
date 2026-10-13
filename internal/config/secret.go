package config

import (
	"fmt"
	"os/exec"
	"strings"
)

// ResolvePassword returns the SMTP password, running password_cmd when set.
func (o *Outbound) ResolvePassword() (string, error) {
	return resolveSecret(o.Password, o.PasswordCmd)
}

// ResolvePassword returns the IMAP password, running password_cmd when set.
func (i *IMAP) ResolvePassword() (string, error) {
	return resolveSecret(i.Password, i.PasswordCmd)
}

// resolveSecret runs cmd through `sh -c` when set (e.g. a macOS Keychain or
// `pass` lookup) and returns its stdout minus the trailing newline; otherwise
// it returns the literal value. The command runs only when a connection is
// actually made, so secret-free commands like render never trigger it.
func resolveSecret(value, cmd string) (string, error) {
	if cmd == "" {
		return value, nil
	}
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("password_cmd failed: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("password_cmd failed: %w", err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
