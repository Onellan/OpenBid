package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDataPathPrefersExplicitEnv(t *testing.T) {
	t.Setenv("DATA_PATH", filepath.Join("custom", "store.db"))
	if got := resolveDataPath(); got != filepath.Join("custom", "store.db") {
		t.Fatalf("expected env data path, got %q", got)
	}
}

func TestResolveDataPathFallsBackToProductionDeploymentRuntime(t *testing.T) {
	t.Setenv("DATA_PATH", "")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	path := filepath.Join("ProductionDeployment", "runtime", "data")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(path, "store.db")
	if err := os.WriteFile(storePath, []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := resolveDataPath(); got != filepath.ToSlash(filepath.Clean("./ProductionDeployment/runtime/data/store.db")) && got != filepath.Join(".", "ProductionDeployment", "runtime", "data", "store.db") && got != "./ProductionDeployment/runtime/data/store.db" {
		t.Fatalf("expected production deployment runtime path, got %q", got)
	}
}

func TestResolveDataPathPrefersProductionDeploymentOverRootDataStore(t *testing.T) {
	t.Setenv("DATA_PATH", "")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	for _, dir := range []string{
		filepath.Join("data"),
		filepath.Join("ProductionDeployment", "runtime", "data"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join("data", "store.db"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("ProductionDeployment", "runtime", "data", "store.db"), []byte("runtime"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := resolveDataPath(); got != "./ProductionDeployment/runtime/data/store.db" {
		t.Fatalf("expected production deployment runtime path to win, got %q", got)
	}
}

func TestResolveDataPathDefaultsToLocalDataStore(t *testing.T) {
	t.Setenv("DATA_PATH", "")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})

	if got := resolveDataPath(); got != "./ProductionDeployment/runtime/data/store.db" {
		t.Fatalf("expected production deployment runtime path default, got %q", got)
	}
}