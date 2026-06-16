package config

import (
	"path/filepath"
	"testing"
)

func TestDirEnvOverride(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/mailotron-test")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/mailotron-test" {
		t.Errorf("Dir = %q, want override", dir)
	}
}

func TestDirDefaultIsDotMailotron(t *testing.T) {
	t.Setenv(EnvConfigDir, "")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != ".mailotron" {
		t.Errorf("default dir base = %q, want .mailotron", filepath.Base(dir))
	}
}

func TestFilePathOverrideWins(t *testing.T) {
	p, err := FilePath("/custom/path.yml")
	if err != nil {
		t.Fatal(err)
	}
	if p != "/custom/path.yml" {
		t.Errorf("FilePath = %q, want override", p)
	}
}

func TestFilePathDefaultName(t *testing.T) {
	t.Setenv(EnvConfigDir, t.TempDir())
	t.Setenv(EnvConfigFile, "")
	p, err := FilePath("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != ConfigFileName {
		t.Errorf("FilePath base = %q, want %q", filepath.Base(p), ConfigFileName)
	}
}
