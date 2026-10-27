package project

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VyxorDev/packrail/internal/lockfile"
	"github.com/VyxorDev/packrail/internal/manifest"
	"github.com/VyxorDev/packrail/internal/registry"
)

func projectTestPackage(name, version string) registry.Package {
	return registry.Package{
		Name:        name,
		Version:     version,
		Description: "test package",
		Source:      "https://github.com/example/" + name,
		Artifacts: map[string]registry.Artifact{
			"any": {
				URL:        "https://github.com/example/" + name + "/releases/download/v" + version + "/" + name + ".tgz",
				SHA256:     strings.Repeat("a", 64),
				Format:     "tgz",
				Entrypoint: name,
			},
		},
		Command: registry.Command{Name: name},
	}
}

func writeProjectState(t *testing.T, dir string, deps map[string]manifest.Dependency, packages ...registry.Package) {
	t.Helper()
	mf := manifest.New()
	for name, dep := range deps {
		if err := manifest.Upsert(&mf, name, dep); err != nil { t.Fatal(err) }
	}
	if err := manifest.Save(filepath.Join(dir, manifest.FileName), mf); err != nil { t.Fatal(err) }
	lf := lockfile.New()
	for _, pkg := range packages {
		if err := lockfile.Upsert(&lf, pkg); err != nil { t.Fatal(err) }
	}
	if err := lockfile.Save(filepath.Join(dir, lockfile.FileName), lf); err != nil { t.Fatal(err) }
}

func TestInitCreatesManifest(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir)
	if err != nil { t.Fatal(err) }
	result, err := m.Init()
	if err != nil { t.Fatal(err) }
	if !result.Created || result.MigratedLock {
		t.Fatalf("unexpected init result: %#v", result)
	}
	mf, err := manifest.Load(filepath.Join(dir, manifest.FileName))
	if err != nil { t.Fatal(err) }
	if len(mf.Dependencies) != 0 {
		t.Fatalf("new manifest has dependencies: %#v", mf.Dependencies)
	}
}

func TestInitMigratesLockOnlyProjectUsingExactVersions(t *testing.T) {
	dir := t.TempDir()
	pkg := projectTestPackage("demo", "1.2.3")
	lf := lockfile.New()
	if err := lockfile.Upsert(&lf, pkg); err != nil { t.Fatal(err) }
	if err := lockfile.Save(filepath.Join(dir, lockfile.FileName), lf); err != nil { t.Fatal(err) }
	m, _ := New(dir)
	result, err := m.Init()
	if err != nil { t.Fatal(err) }
	if !result.MigratedLock {
		t.Fatal("expected lock-only migration")
	}
	mf, err := manifest.Load(filepath.Join(dir, manifest.FileName))
	if err != nil { t.Fatal(err) }
	dep := mf.Dependencies["demo"]
	if dep.Constraint != "1.2.3" || dep.Repo != "" {
		t.Fatalf("migration invented or lost intent: %#v", dep)
	}
}

func TestAddResolvesInstallsThenCommitsProjectState(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(dir)
	pkg := projectTestPackage("demo", "1.0.0")
	resolveCalls, ensureCalls := 0, 0
	m.Resolve = func(name string, dep manifest.Dependency) (registry.Package, error) {
		resolveCalls++
		if name != "demo" || dep.Constraint != "^1" {
			t.Fatalf("unexpected resolver input: %q %#v", name, dep)
		}
		return pkg, nil
	}
	m.Ensure = func(got registry.Package) (bool, error) {
		ensureCalls++
		if got.Version != pkg.Version { t.Fatalf("unexpected package: %#v", got) }
		return true, nil
	}
	result, err := m.Add("demo@^1")
	if err != nil { t.Fatal(err) }
	if !result.Installed || resolveCalls != 1 || ensureCalls != 1 {
		t.Fatalf("unexpected add result/calls: %#v resolve=%d ensure=%d", result, resolveCalls, ensureCalls)
	}
	mf, err := manifest.Load(filepath.Join(dir, manifest.FileName))
	if err != nil { t.Fatal(err) }
	lf, err := lockfile.Load(filepath.Join(dir, lockfile.FileName))
	if err != nil { t.Fatal(err) }
	if mf.Dependencies["demo"].Constraint != "^1" || len(lf.Packages) != 1 || lf.Packages[0].Version != "1.0.0" {
		t.Fatalf("unexpected project state: %#v %#v", mf, lf)
	}
	if err := CheckAgreement(mf, lf); err != nil { t.Fatal(err) }
}

func TestFailedInstallLeavesProjectFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(dir)
	pkg := projectTestPackage("demo", "1.0.0")
	m.Resolve = func(string, manifest.Dependency) (registry.Package, error) { return pkg, nil }
	m.Ensure = func(registry.Package) (bool, error) { return false, errors.New("simulated checksum failure") }
	if _, err := m.Add("demo"); err == nil {
		t.Fatal("expected add failure")
	}
	if _, err := os.Stat(filepath.Join(dir, manifest.FileName)); !os.IsNotExist(err) {
		t.Fatalf("manifest should not have been committed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, lockfile.FileName)); !os.IsNotExist(err) {
		t.Fatalf("lockfile should not have been committed, stat err=%v", err)
	}
}

func TestManifestCommitFailureRollsBackLockfile(t *testing.T) {
	dir := t.TempDir()
	alpha := projectTestPackage("alpha", "1.0.0")
	writeProjectState(t, dir, map[string]manifest.Dependency{"alpha": {Constraint: "1.0.0"}}, alpha)
	oldManifest, _ := os.ReadFile(filepath.Join(dir, manifest.FileName))
	oldLock, _ := os.ReadFile(filepath.Join(dir, lockfile.FileName))

	m, _ := New(dir)
	beta := projectTestPackage("beta", "2.0.0")
	m.Resolve = func(string, manifest.Dependency) (registry.Package, error) { return beta, nil }
	m.Ensure = func(registry.Package) (bool, error) { return true, nil }
	m.SaveManifest = func(string, manifest.File) error { return errors.New("simulated manifest commit failure") }
	if _, err := m.Add("beta@2.0.0"); err == nil {
		t.Fatal("expected commit failure")
	}
	newManifest, _ := os.ReadFile(filepath.Join(dir, manifest.FileName))
	newLock, _ := os.ReadFile(filepath.Join(dir, lockfile.FileName))
	if !bytes.Equal(oldManifest, newManifest) || !bytes.Equal(oldLock, newLock) {
		t.Fatalf("old project pair was not restored\nmanifest before=%s after=%s\nlock before=%s after=%s", oldManifest, newManifest, oldLock, newLock)
	}
	if _, err := os.Stat(filepath.Join(dir, transactionFile)); !os.IsNotExist(err) {
		t.Fatalf("transaction journal was not cleaned up: %v", err)
	}
}

func TestSyncNeverCallsResolver(t *testing.T) {
	dir := t.TempDir()
	pkg := projectTestPackage("demo", "1.0.0")
	writeProjectState(t, dir, map[string]manifest.Dependency{"demo": {Constraint: "*"}}, pkg)
	m, _ := New(dir)
	m.Resolve = func(string, manifest.Dependency) (registry.Package, error) {
		t.Fatal("sync must not re-resolve dependencies")
		return registry.Package{}, nil
	}
	ensureCalls := 0
	m.Ensure = func(got registry.Package) (bool, error) {
		ensureCalls++
		if got.Version != "1.0.0" { t.Fatalf("sync drifted to %s", got.Version) }
		return true, nil
	}
	result, err := m.Sync()
	if err != nil { t.Fatal(err) }
	if result.Installed != 1 || ensureCalls != 1 {
		t.Fatalf("unexpected sync result: %#v calls=%d", result, ensureCalls)
	}
}

func TestSyncSupportsLegacyLockOnlyProject(t *testing.T) {
	dir := t.TempDir()
	pkg := projectTestPackage("demo", "1.0.0")
	lf := lockfile.New()
	if err := lockfile.Upsert(&lf, pkg); err != nil { t.Fatal(err) }
	if err := lockfile.Save(filepath.Join(dir, lockfile.FileName), lf); err != nil { t.Fatal(err) }
	m, _ := New(dir)
	m.Ensure = func(registry.Package) (bool, error) { return false, nil }
	result, err := m.Sync()
	if err != nil { t.Fatal(err) }
	if !result.LockOnly || result.Packages != 1 {
		t.Fatalf("unexpected legacy sync result: %#v", result)
	}
}

func TestUpdateReresolvesAndKeepsManifestIntent(t *testing.T) {
	dir := t.TempDir()
	oldPkg := projectTestPackage("demo", "1.0.0")
	writeProjectState(t, dir, map[string]manifest.Dependency{"demo": {Constraint: "^1"}}, oldPkg)
	m, _ := New(dir)
	newPkg := projectTestPackage("demo", "1.1.0")
	resolveCalls := 0
	m.Resolve = func(name string, dep manifest.Dependency) (registry.Package, error) {
		resolveCalls++
		if dep.Constraint != "^1" { t.Fatalf("intent changed: %#v", dep) }
		return newPkg, nil
	}
	m.Ensure = func(registry.Package) (bool, error) { return true, nil }
	if _, err := m.Update(nil); err != nil { t.Fatal(err) }
	if resolveCalls != 1 { t.Fatalf("resolver calls = %d", resolveCalls) }
	mf, err := manifest.Load(filepath.Join(dir, manifest.FileName))
	if err != nil { t.Fatal(err) }
	lf, err := lockfile.Load(filepath.Join(dir, lockfile.FileName))
	if err != nil { t.Fatal(err) }
	if mf.Dependencies["demo"].Constraint != "^1" || lf.Packages[0].Version != "1.1.0" {
		t.Fatalf("unexpected updated state: %#v %#v", mf, lf)
	}
}

func TestCheckAgreementRejectsDriftAndExtraPackages(t *testing.T) {
	mf := manifest.New()
	if err := manifest.Upsert(&mf, "demo", manifest.Dependency{Constraint: "^1"}); err != nil { t.Fatal(err) }
	lf := lockfile.New()
	if err := lockfile.Upsert(&lf, projectTestPackage("demo", "2.0.0")); err != nil { t.Fatal(err) }
	if err := CheckAgreement(mf, lf); err == nil {
		t.Fatal("expected constraint drift to fail")
	}
	lf = lockfile.New()
	if err := lockfile.Upsert(&lf, projectTestPackage("demo", "1.0.0")); err != nil { t.Fatal(err) }
	if err := lockfile.Upsert(&lf, projectTestPackage("extra", "1.0.0")); err != nil { t.Fatal(err) }
	if err := CheckAgreement(mf, lf); err == nil {
		t.Fatal("expected extra lock entry to fail")
	}
}

func TestCheckAgreementRejectsDirectRepoMismatch(t *testing.T) {
	mf := manifest.New()
	if err := manifest.Upsert(&mf, "demo", manifest.Dependency{Constraint: "1.0.0", Repo: "expected/demo"}); err != nil { t.Fatal(err) }
	lf := lockfile.New()
	if err := lockfile.Upsert(&lf, projectTestPackage("demo", "1.0.0")); err != nil { t.Fatal(err) }
	if err := CheckAgreement(mf, lf); err == nil {
		t.Fatal("expected direct repository mismatch to fail")
	}
}

func TestRemoveChangesProjectStateWithoutCallingGlobalUninstall(t *testing.T) {
	dir := t.TempDir()
	pkg := projectTestPackage("demo", "1.0.0")
	writeProjectState(t, dir, map[string]manifest.Dependency{"demo": {Constraint: "1.0.0"}}, pkg)
	m, _ := New(dir)
	result, err := m.Remove("demo", false)
	if err != nil { t.Fatal(err) }
	if result.Uninstalled { t.Fatal("project remove unexpectedly uninstalled globally") }
	mf, err := manifest.Load(filepath.Join(dir, manifest.FileName))
	if err != nil { t.Fatal(err) }
	lf, err := lockfile.Load(filepath.Join(dir, lockfile.FileName))
	if err != nil { t.Fatal(err) }
	if len(mf.Dependencies) != 0 || len(lf.Packages) != 0 {
		t.Fatalf("remove did not prune project state: %#v %#v", mf, lf)
	}
}
