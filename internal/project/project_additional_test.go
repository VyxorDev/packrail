package project

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/VyxorDev/packrail/internal/lockfile"
	"github.com/VyxorDev/packrail/internal/manifest"
	"github.com/VyxorDev/packrail/internal/registry"
)

func TestResolverFailureLeavesProjectFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(dir)
	m.Resolve = func(string, manifest.Dependency) (registry.Package, error) {
		return registry.Package{}, errors.New("resolver unavailable")
	}
	if _, err := m.Add("demo"); err == nil {
		t.Fatal("expected resolver failure")
	}
	for _, name := range []string{manifest.FileName, lockfile.FileName, transactionFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should not exist after resolver failure: %v", name, err)
		}
	}
}

func TestAddDirectGitHubDependencyPersistsRepositoryIntent(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(dir)
	pkg := projectTestPackage("demo", "1.2.3")
	pkg.Source = "https://github.com/Owner/Repo"
	artifact := pkg.Artifacts["any"]
	artifact.URL = "https://github.com/Owner/Repo/releases/download/v1.2.3/demo.tgz"
	pkg.Artifacts["any"] = artifact

	m.Resolve = func(name string, dep manifest.Dependency) (registry.Package, error) {
		if name != "" {
			t.Fatalf("direct dependency unexpectedly supplied registry name %q", name)
		}
		if dep.Repo != "Owner/Repo" || dep.Constraint != "1.2.3" {
			t.Fatalf("unexpected direct dependency: %#v", dep)
		}
		return pkg, nil
	}
	m.Ensure = func(registry.Package) (bool, error) { return true, nil }
	if _, err := m.Add("github:Owner/Repo@1.2.3"); err != nil {
		t.Fatal(err)
	}
	mf, err := manifest.Load(filepath.Join(dir, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	dep := mf.Dependencies["demo"]
	if dep.Repo != "Owner/Repo" || dep.Constraint != "1.2.3" {
		t.Fatalf("direct repository intent was not preserved: %#v", dep)
	}
	lf, err := lockfile.Load(filepath.Join(dir, lockfile.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckAgreement(mf, lf); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverPreparedTransactionRestoresOldPair(t *testing.T) {
	dir := t.TempDir()
	oldPkg := projectTestPackage("old", "1.0.0")
	writeProjectState(t, dir, map[string]manifest.Dependency{"old": {Constraint: "1.0.0"}}, oldPkg)
	oldManifest, _ := os.ReadFile(filepath.Join(dir, manifest.FileName))
	oldLock, _ := os.ReadFile(filepath.Join(dir, lockfile.FileName))

	m, _ := New(dir)
	if _, err := m.beginTransaction(); err != nil {
		t.Fatal(err)
	}
	newPkg := projectTestPackage("new", "2.0.0")
	newManifest := manifest.New()
	if err := manifest.Upsert(&newManifest, "new", manifest.Dependency{Constraint: "2.0.0"}); err != nil {
		t.Fatal(err)
	}
	newLock := lockfile.New()
	if err := lockfile.Upsert(&newLock, newPkg); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Save(filepath.Join(dir, manifest.FileName), newManifest); err != nil {
		t.Fatal(err)
	}
	if err := lockfile.Save(filepath.Join(dir, lockfile.FileName), newLock); err != nil {
		t.Fatal(err)
	}

	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	recoveredManifest, _ := os.ReadFile(filepath.Join(dir, manifest.FileName))
	recoveredLock, _ := os.ReadFile(filepath.Join(dir, lockfile.FileName))
	if !bytes.Equal(oldManifest, recoveredManifest) || !bytes.Equal(oldLock, recoveredLock) {
		t.Fatalf("prepared transaction did not restore old project state")
	}
	if _, err := os.Stat(filepath.Join(dir, transactionFile)); !os.IsNotExist(err) {
		t.Fatalf("transaction journal remains after recovery: %v", err)
	}
}

func TestUpdateSelectedPackageDoesNotReresolveOthers(t *testing.T) {
	dir := t.TempDir()
	alphaOld := projectTestPackage("alpha", "1.0.0")
	beta := projectTestPackage("beta", "2.0.0")
	writeProjectState(t, dir, map[string]manifest.Dependency{
		"alpha": {Constraint: "^1"},
		"beta":  {Constraint: "2.0.0"},
	}, alphaOld, beta)

	m, _ := New(dir)
	alphaNew := projectTestPackage("alpha", "1.1.0")
	calls := 0
	m.Resolve = func(name string, dep manifest.Dependency) (registry.Package, error) {
		calls++
		if name != "alpha" {
			t.Fatalf("update selected package unexpectedly resolved %q", name)
		}
		return alphaNew, nil
	}
	m.Ensure = func(pkg registry.Package) (bool, error) {
		if pkg.Name != "alpha" {
			t.Fatalf("update selected package unexpectedly installed %q", pkg.Name)
		}
		return true, nil
	}
	if _, err := m.Update([]string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("resolver called %d times", calls)
	}
	lf, err := lockfile.Load(filepath.Join(dir, lockfile.FileName))
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{}
	for _, pkg := range lf.Packages {
		versions[pkg.Name] = pkg.Version
	}
	if versions["alpha"] != "1.1.0" || versions["beta"] != "2.0.0" {
		t.Fatalf("selected update changed the wrong packages: %#v", versions)
	}
}
