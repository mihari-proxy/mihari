package panel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFileRootPrefersRootThenDistThenNested(t *testing.T) {
	root := t.TempDir()

	// Nested only (metacubexd GitHub zipball shape before hoist).
	nestedBuild := filepath.Join(root, "nested")
	nestedApp := filepath.Join(nestedBuild, "metacubexd-abc")
	if err := os.MkdirAll(nestedApp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedApp, "index.html"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveFileRoot(nestedBuild); got != nestedApp {
		t.Fatalf("nested got=%q want=%q", got, nestedApp)
	}

	// dist/ preferred over deeper nested when root has no index.
	distBuild := filepath.Join(root, "dist-build")
	if err := os.MkdirAll(filepath.Join(distBuild, "dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distBuild, "dist", "index.html"), []byte("dist"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveFileRoot(distBuild); got != filepath.Join(distBuild, "dist") {
		t.Fatalf("dist got=%q", got)
	}

	// Root index wins.
	if err := os.WriteFile(filepath.Join(distBuild, "index.html"), []byte("root"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveFileRoot(distBuild); got != distBuild {
		t.Fatalf("root got=%q", got)
	}
}

func TestHoistSingleRootDirLiftsGitHubZipball(t *testing.T) {
	dest := t.TempDir()
	nested := filepath.Join(dest, "metacubexd-sha")
	if err := os.MkdirAll(filepath.Join(nested, "_nuxt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "index.html"), []byte("xd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "_nuxt", "app.js"), []byte("js"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hoistSingleRootDir(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "index.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "_nuxt", "app.js")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatalf("nested dir should be removed, err=%v", err)
	}
}

func TestUIMount(t *testing.T) {
	if got := UIMount("metacubexd"); got != "/__mihari/panels/metacubexd" {
		t.Fatalf("got=%q", got)
	}
}
