package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/VyxorDev/packrail/internal/install"
	"github.com/VyxorDev/packrail/internal/lockfile"
	"github.com/VyxorDev/packrail/internal/manifest"
	"github.com/VyxorDev/packrail/internal/publisher"
	"github.com/VyxorDev/packrail/internal/registry"
)

const (
	transactionFile    = ".packrail.transaction"
	transactionVersion = 1
	maxTransactionSize = 40 << 20
)

type ResolveFunc func(name string, dep manifest.Dependency) (registry.Package, error)
type EnsureFunc func(pkg registry.Package) (bool, error)
type SaveManifestFunc func(path string, file manifest.File) error
type SaveLockFunc func(path string, file lockfile.File) error

type Manager struct {
	Dir          string
	Resolve      ResolveFunc
	Ensure       EnsureFunc
	SaveManifest SaveManifestFunc
	SaveLock     SaveLockFunc
}

type InitResult struct {
	Path         string
	Created      bool
	MigratedLock bool
	Dependencies int
}

type AddResult struct {
	Package   registry.Package
	Installed bool
}

type SyncResult struct {
	Installed int
	Current   int
	Packages  int
	LockOnly  bool
}

type SyncPlan struct {
	Packages     int
	LockOnly     bool
	NeedsInstall []registry.Package
	Current      []registry.Package
}

type UpdateResult struct {
	Packages  []registry.Package
	Installed int
}

type RemoveResult struct {
	Name        string
	Uninstalled bool
}

func New(dir string) (*Manager, error) {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve project directory: %w", err)
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory: %w", err)
	}
	return &Manager{
		Dir:          abs,
		Resolve:      defaultResolve,
		Ensure:       defaultEnsure,
		SaveManifest: manifest.Save,
		SaveLock:     lockfile.Save,
	}, nil
}

func (m *Manager) ManifestPath() string { return filepath.Join(m.Dir, manifest.FileName) }
func (m *Manager) LockPath() string     { return filepath.Join(m.Dir, lockfile.FileName) }
func (m *Manager) transactionPath() string { return filepath.Join(m.Dir, transactionFile) }

// Recover repairs an operation interrupted while committing project metadata.
// A prepared journal means the old manifest/lock pair must be restored; a
// committed journal means both new files were already durable and only cleanup
// was interrupted.
func (m *Manager) Recover() error {
	path := m.transactionPath()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat project transaction: %w", err)
	}
	if info.IsDir() || info.Size() > maxTransactionSize {
		return fmt.Errorf("invalid Packrail transaction journal %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read project transaction: %w", err)
	}
	var tx transactionRecord
	if err := json.Unmarshal(data, &tx); err != nil {
		return fmt.Errorf("decode project transaction %s: %w", path, err)
	}
	if tx.Version != transactionVersion || (tx.Phase != "prepared" && tx.Phase != "committed") {
		return fmt.Errorf("unsupported or corrupt Packrail transaction journal %s", path)
	}
	if tx.Phase == "prepared" {
		if err := restoreSnapshot(m.LockPath(), tx.Lock); err != nil {
			return fmt.Errorf("recover lockfile: %w", err)
		}
		if err := restoreSnapshot(m.ManifestPath(), tx.Manifest); err != nil {
			return fmt.Errorf("recover manifest: %w", err)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove recovered transaction journal: %w", err)
	}
	return nil
}

func (m *Manager) Init() (InitResult, error) {
	if err := m.Recover(); err != nil {
		return InitResult{}, err
	}
	manifestPath := m.ManifestPath()
	if _, err := os.Stat(manifestPath); err == nil {
		file, loadErr := manifest.Load(manifestPath)
		if loadErr != nil {
			return InitResult{}, loadErr
		}
		return InitResult{Path: manifestPath, Dependencies: len(file.Dependencies)}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return InitResult{}, err
	}

	file := manifest.New()
	migrated := false
	if _, err := os.Stat(m.LockPath()); err == nil {
		locked, err := lockfile.Load(m.LockPath())
		if err != nil {
			return InitResult{}, err
		}
		for _, pkg := range locked.Packages {
			// An exact version is the only dependency intent a v0.9 lockfile
			// proves. Do not invent wildcard/range or direct-vs-registry intent.
			if err := manifest.Upsert(&file, pkg.Name, manifest.Dependency{Constraint: pkg.Version}); err != nil {
				return InitResult{}, fmt.Errorf("migrate %s: %w", pkg.Name, err)
			}
		}
		if err := CheckAgreement(file, locked); err != nil {
			return InitResult{}, fmt.Errorf("migrate lockfile: %w", err)
		}
		migrated = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return InitResult{}, err
	}
	if err := m.commitManifest(file); err != nil {
		return InitResult{}, err
	}
	return InitResult{Path: manifestPath, Created: true, MigratedLock: migrated, Dependencies: len(file.Dependencies)}, nil
}

func (m *Manager) Add(target string) (AddResult, error) {
	if err := m.Recover(); err != nil {
		return AddResult{}, err
	}
	manifestPath, lockPath := m.ManifestPath(), m.LockPath()
	mf, mfExists, err := loadManifestOptional(manifestPath)
	if err != nil {
		return AddResult{}, err
	}
	if !mfExists {
		if _, statErr := os.Stat(lockPath); statErr == nil {
			return AddResult{}, fmt.Errorf("%s exists without %s; run 'prl init' first to migrate the v0.9 project safely", lockfile.FileName, manifest.FileName)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return AddResult{}, statErr
		}
		mf = manifest.New()
	}
	lf, _, err := loadLockOptional(lockPath)
	if err != nil {
		return AddResult{}, err
	}

	name, dep, pkg, err := m.resolveTarget(target)
	if err != nil {
		return AddResult{}, err
	}
	if _, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH); err != nil {
		return AddResult{}, err
	}
	prospectiveManifest := manifest.Clone(mf)
	if err := manifest.Upsert(&prospectiveManifest, name, dep); err != nil {
		return AddResult{}, err
	}
	prospectiveLock, err := cloneLock(lf)
	if err != nil {
		return AddResult{}, err
	}
	if err := lockfile.Upsert(&prospectiveLock, pkg); err != nil {
		return AddResult{}, fmt.Errorf("resolve lockfile: %w", err)
	}
	if err := lockfile.Validate(prospectiveLock); err != nil {
		return AddResult{}, fmt.Errorf("validate prospective lockfile: %w", err)
	}
	if err := CheckAgreement(prospectiveManifest, prospectiveLock); err != nil {
		return AddResult{}, err
	}

	installed, err := m.Ensure(pkg)
	if err != nil {
		return AddResult{}, fmt.Errorf("install %s %s: %w", pkg.Name, pkg.Version, err)
	}
	if err := m.commitPair(prospectiveManifest, prospectiveLock); err != nil {
		return AddResult{}, err
	}
	return AddResult{Package: pkg, Installed: installed}, nil
}

func (m *Manager) Sync() (SyncResult, error) {
	if err := m.Recover(); err != nil {
		return SyncResult{}, err
	}
	lf, err := lockfile.Load(m.LockPath())
	if err != nil {
		return SyncResult{}, err
	}
	mf, mfExists, err := loadManifestOptional(m.ManifestPath())
	if err != nil {
		return SyncResult{}, err
	}
	if mfExists {
		if err := CheckAgreement(mf, lf); err != nil {
			return SyncResult{}, fmt.Errorf("project state is out of date: %w; run 'prl update' to resolve %s into %s", err, manifest.FileName, lockfile.FileName)
		}
	}
	for _, pkg := range lf.Packages {
		if _, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH); err != nil {
			return SyncResult{}, fmt.Errorf("lockfile package %s: %w", pkg.Name, err)
		}
	}
	result := SyncResult{Packages: len(lf.Packages), LockOnly: !mfExists}
	for _, pkg := range lf.Packages {
		changed, err := m.Ensure(pkg)
		if err != nil {
			return SyncResult{}, fmt.Errorf("sync %s %s: %w", pkg.Name, pkg.Version, err)
		}
		if changed { result.Installed++ } else { result.Current++ }
	}
	return result, nil
}

// PlanSync inspects the current project and installed state without recovering
// journals, downloading artifacts, installing packages, or changing project files.
func (m *Manager) PlanSync() (SyncPlan, error) {
	if _, err := os.Stat(m.transactionPath()); err == nil {
		return SyncPlan{}, fmt.Errorf("a project transaction is pending recovery; run 'prl sync' without --dry-run first")
	} else if !errors.Is(err, os.ErrNotExist) {
		return SyncPlan{}, fmt.Errorf("stat project transaction: %w", err)
	}

	lf, err := lockfile.Load(m.LockPath())
	if err != nil {
		return SyncPlan{}, err
	}
	mf, mfExists, err := loadManifestOptional(m.ManifestPath())
	if err != nil {
		return SyncPlan{}, err
	}
	if mfExists {
		if err := CheckAgreement(mf, lf); err != nil {
			return SyncPlan{}, fmt.Errorf("project state is out of date: %w; run 'prl update' to resolve %s into %s", err, manifest.FileName, lockfile.FileName)
		}
	}

	plan := SyncPlan{Packages: len(lf.Packages), LockOnly: !mfExists}
	for _, pkg := range lf.Packages {
		if _, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH); err != nil {
			return SyncPlan{}, fmt.Errorf("lockfile package %s: %w", pkg.Name, err)
		}
		current, err := install.IsCurrent(pkg)
		if err != nil {
			return SyncPlan{}, fmt.Errorf("inspect %s %s: %w", pkg.Name, pkg.Version, err)
		}
		if current {
			plan.Current = append(plan.Current, pkg)
		} else {
			plan.NeedsInstall = append(plan.NeedsInstall, pkg)
		}
	}
	return plan, nil
}

func (m *Manager) Update(names []string) (UpdateResult, error) {
	if err := m.Recover(); err != nil {
		return UpdateResult{}, err
	}
	mf, err := manifest.Load(m.ManifestPath())
	if err != nil {
		return UpdateResult{}, err
	}
	lf, _, err := loadLockOptional(m.LockPath())
	if err != nil {
		return UpdateResult{}, err
	}

	selected, err := selectedDependencies(mf, names)
	if err != nil {
		return UpdateResult{}, err
	}
	prospective, err := cloneLock(lf)
	if err != nil {
		return UpdateResult{}, err
	}
	resolved := make([]registry.Package, 0, len(selected))
	for _, name := range selected {
		pkg, err := m.Resolve(name, mf.Dependencies[name])
		if err != nil {
			return UpdateResult{}, fmt.Errorf("resolve %s: %w", name, err)
		}
		if _, err := registry.ArtifactFor(pkg, runtime.GOOS, runtime.GOARCH); err != nil {
			return UpdateResult{}, err
		}
		if err := lockfile.Upsert(&prospective, pkg); err != nil {
			return UpdateResult{}, fmt.Errorf("lock %s: %w", name, err)
		}
		resolved = append(resolved, pkg)
	}
	if err := lockfile.Validate(prospective); err != nil {
		return UpdateResult{}, fmt.Errorf("validate prospective lockfile: %w", err)
	}
	if err := CheckAgreement(mf, prospective); err != nil {
		return UpdateResult{}, fmt.Errorf("cannot update only the selected dependencies: %w", err)
	}
	result := UpdateResult{Packages: resolved}
	for _, pkg := range resolved {
		changed, err := m.Ensure(pkg)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("install %s %s: %w", pkg.Name, pkg.Version, err)
		}
		if changed { result.Installed++ }
	}
	if err := m.commitLock(prospective); err != nil {
		return UpdateResult{}, err
	}
	return result, nil
}

func (m *Manager) Remove(name string, uninstall bool) (RemoveResult, error) {
	if err := m.Recover(); err != nil {
		return RemoveResult{}, err
	}
	mf, err := manifest.Load(m.ManifestPath())
	if err != nil {
		return RemoveResult{}, err
	}
	lf, _, err := loadLockOptional(m.LockPath())
	if err != nil {
		return RemoveResult{}, err
	}
	prospectiveManifest := manifest.Clone(mf)
	removed, err := manifest.Remove(&prospectiveManifest, name)
	if err != nil {
		return RemoveResult{}, err
	}
	if !removed {
		return RemoveResult{}, fmt.Errorf("package %q is not in %s", name, manifest.FileName)
	}
	prospectiveLock, err := cloneLock(lf)
	if err != nil {
		return RemoveResult{}, err
	}
	_, _ = lockfile.Remove(&prospectiveLock, name)
	if err := CheckAgreement(prospectiveManifest, prospectiveLock); err != nil {
		return RemoveResult{}, err
	}
	if err := m.commitPair(prospectiveManifest, prospectiveLock); err != nil {
		return RemoveResult{}, err
	}
	result := RemoveResult{Name: strings.ToLower(strings.TrimSpace(name))}
	if uninstall {
		if _, err := install.Remove(name); err != nil {
			return result, fmt.Errorf("project dependency removed, but global uninstall failed: %w", err)
		}
		result.Uninstalled = true
	}
	return result, nil
}

// CheckAgreement verifies that manifest intent and resolved lock state describe
// exactly the same project dependencies. Sync uses this check but never repairs
// disagreement by re-resolving.
func CheckAgreement(mf manifest.File, lf lockfile.File) error {
	if err := manifest.Validate(&mf); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := lockfile.Validate(lf); err != nil {
		return fmt.Errorf("lockfile: %w", err)
	}
	locked := make(map[string]registry.Package, len(lf.Packages))
	for _, pkg := range lf.Packages {
		locked[strings.ToLower(pkg.Name)] = pkg
	}
	for name, dep := range mf.Dependencies {
		pkg, ok := locked[name]
		if !ok {
			return fmt.Errorf("dependency %s is missing from %s", name, lockfile.FileName)
		}
		okVersion, err := registry.ConstraintSatisfied(pkg.Version, dep.Constraint)
		if err != nil {
			return fmt.Errorf("dependency %s constraint: %w", name, err)
		}
		if !okVersion {
			return fmt.Errorf("dependency %s requests %s but lockfile contains %s", name, dep.Constraint, pkg.Version)
		}
		if dep.Repo != "" {
			expected := normalizeGitHubSource("https://github.com/" + dep.Repo)
			if !strings.EqualFold(normalizeGitHubSource(pkg.Source), expected) {
				return fmt.Errorf("dependency %s requests github:%s but lockfile source is %s", name, dep.Repo, pkg.Source)
			}
		}
		delete(locked, name)
	}
	if len(locked) > 0 {
		extra := make([]string, 0, len(locked))
		for name := range locked { extra = append(extra, name) }
		sort.Strings(extra)
		return fmt.Errorf("%s contains dependencies not present in %s: %s", lockfile.FileName, manifest.FileName, strings.Join(extra, ", "))
	}
	return nil
}

func (m *Manager) resolveTarget(target string) (string, manifest.Dependency, registry.Package, error) {
	value := strings.TrimSpace(target)
	if value == "" {
		return "", manifest.Dependency{}, registry.Package{}, fmt.Errorf("dependency target is required")
	}
	if strings.HasPrefix(strings.ToLower(value), "github:") {
		body := strings.TrimSpace(value[len("github:"):])
		constraint := "*"
		if at := strings.LastIndexByte(body, '@'); at > strings.LastIndexByte(body, '/') {
			constraint = strings.TrimSpace(body[at+1:])
			body = strings.TrimSpace(body[:at])
		}
		dep := manifest.Dependency{Constraint: constraint, Repo: body}
		pkg, err := m.Resolve("", dep)
		if err != nil {
			return "", manifest.Dependency{}, registry.Package{}, err
		}
		return strings.ToLower(pkg.Name), dep, pkg, nil
	}
	name, constraint, err := registry.ParseDependencyTarget(value)
	if err != nil {
		return "", manifest.Dependency{}, registry.Package{}, err
	}
	dep := manifest.Dependency{Constraint: constraint}
	pkg, err := m.Resolve(name, dep)
	if err != nil {
		return "", manifest.Dependency{}, registry.Package{}, err
	}
	return name, dep, pkg, nil
}

func defaultResolve(name string, dep manifest.Dependency) (registry.Package, error) {
	if dep.Repo == "" {
		_ = registry.RefreshIfStale()
		return registry.FindSatisfied(name, dep.Constraint)
	}
	client := publisher.New()
	var draft publisher.Draft
	var err error
	if registry.IsExactConstraint(dep.Constraint) {
		draft, err = client.GenerateRelease(dep.Repo, dep.Constraint, "")
	} else {
		draft, err = client.Generate(dep.Repo, "")
	}
	if err != nil {
		return registry.Package{}, err
	}
	if !draft.Complete {
		return registry.Package{}, fmt.Errorf("GitHub release for %s could not be inferred safely", dep.Repo)
	}
	pkg := draft.Package
	if name != "" && !strings.EqualFold(name, pkg.Name) {
		return registry.Package{}, fmt.Errorf("github:%s resolved package name %s, expected %s", dep.Repo, pkg.Name, name)
	}
	ok, err := registry.ConstraintSatisfied(pkg.Version, dep.Constraint)
	if err != nil {
		return registry.Package{}, err
	}
	if !ok {
		return registry.Package{}, fmt.Errorf("github:%s resolved %s, which does not satisfy %s", dep.Repo, pkg.Version, dep.Constraint)
	}
	if err := registry.ValidatePackage(pkg); err != nil {
		return registry.Package{}, err
	}
	if err := registry.ValidateGitHubProvenance(pkg); err != nil {
		return registry.Package{}, err
	}
	return pkg, nil
}

func defaultEnsure(pkg registry.Package) (bool, error) {
	current, err := install.IsCurrent(pkg)
	if err != nil {
		return false, err
	}
	if current {
		return false, nil
	}
	// Install is deliberately non-destructive with respect to older versions.
	// This preserves a working old project state if a later metadata commit
	// fails; garbage collection can safely happen after project commit later.
	if err := install.Install(pkg); err != nil {
		return false, err
	}
	return true, nil
}

func selectedDependencies(mf manifest.File, names []string) ([]string, error) {
	if len(names) == 0 {
		return manifest.Names(mf), nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		if _, ok := mf.Dependencies[name]; !ok {
			return nil, fmt.Errorf("package %q is not in %s", raw, manifest.FileName)
		}
		if !seen[name] { seen[name] = true; out = append(out, name) }
	}
	sort.Strings(out)
	return out, nil
}

func loadManifestOptional(path string) (manifest.File, bool, error) {
	file, err := manifest.Load(path)
	if err == nil { return file, true, nil }
	if errors.Is(err, os.ErrNotExist) { return manifest.New(), false, nil }
	return manifest.File{}, false, err
}

func loadLockOptional(path string) (lockfile.File, bool, error) {
	file, err := lockfile.Load(path)
	if err == nil { return file, true, nil }
	if errors.Is(err, os.ErrNotExist) { return lockfile.New(), false, nil }
	return lockfile.File{}, false, err
}

func cloneLock(file lockfile.File) (lockfile.File, error) {
	data, err := json.Marshal(file)
	if err != nil { return lockfile.File{}, err }
	var out lockfile.File
	if err := json.Unmarshal(data, &out); err != nil { return lockfile.File{}, err }
	return out, nil
}

func normalizeGitHubSource(value string) string {
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(value), "/"), ".git")
}

type fileSnapshot struct {
	Exists bool   `json:"exists"`
	Data   []byte `json:"data,omitempty"`
}

type transactionRecord struct {
	Version  int          `json:"version"`
	Phase    string       `json:"phase"`
	Manifest fileSnapshot `json:"manifest"`
	Lock     fileSnapshot `json:"lock"`
}

func snapshot(path string) (fileSnapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) { return fileSnapshot{}, nil }
	if err != nil { return fileSnapshot{}, err }
	return fileSnapshot{Exists: true, Data: data}, nil
}

func (m *Manager) beginTransaction() (transactionRecord, error) {
	manifestSnapshot, err := snapshot(m.ManifestPath())
	if err != nil { return transactionRecord{}, err }
	lockSnapshot, err := snapshot(m.LockPath())
	if err != nil { return transactionRecord{}, err }
	tx := transactionRecord{Version: transactionVersion, Phase: "prepared", Manifest: manifestSnapshot, Lock: lockSnapshot}
	if err := m.writeTransaction(tx); err != nil { return transactionRecord{}, err }
	return tx, nil
}

func (m *Manager) writeTransaction(tx transactionRecord) error {
	data, err := json.Marshal(tx)
	if err != nil { return err }
	return writeAtomicRaw(m.transactionPath(), append(data, '\n'))
}

func (m *Manager) rollback(tx transactionRecord) error {
	var failures []string
	if err := restoreSnapshot(m.LockPath(), tx.Lock); err != nil { failures = append(failures, "lockfile: "+err.Error()) }
	if err := restoreSnapshot(m.ManifestPath(), tx.Manifest); err != nil { failures = append(failures, "manifest: "+err.Error()) }
	if err := os.Remove(m.transactionPath()); err != nil && !os.IsNotExist(err) { failures = append(failures, "journal: "+err.Error()) }
	if len(failures) > 0 { return fmt.Errorf("rollback failed: %s", strings.Join(failures, "; ")) }
	return nil
}

func (m *Manager) finishTransaction(tx transactionRecord) error {
	tx.Phase = "committed"
	if err := m.writeTransaction(tx); err != nil { return err }
	if err := os.Remove(m.transactionPath()); err != nil && !os.IsNotExist(err) {
		// The committed marker makes a leftover journal harmless: Recover keeps
		// the new state and only removes the marker.
		return nil
	}
	return nil
}

func (m *Manager) commitPair(mf manifest.File, lf lockfile.File) error {
	tx, err := m.beginTransaction()
	if err != nil { return fmt.Errorf("begin project transaction: %w", err) }
	if err := m.SaveLock(m.LockPath(), lf); err != nil {
		_ = m.rollback(tx)
		return fmt.Errorf("save %s: %w", lockfile.FileName, err)
	}
	if err := m.SaveManifest(m.ManifestPath(), mf); err != nil {
		rollbackErr := m.rollback(tx)
		if rollbackErr != nil { return fmt.Errorf("save %s: %v; %v", manifest.FileName, err, rollbackErr) }
		return fmt.Errorf("save %s: %w", manifest.FileName, err)
	}
	if err := m.finishTransaction(tx); err != nil {
		rollbackErr := m.rollback(tx)
		if rollbackErr != nil { return fmt.Errorf("commit project transaction: %v; %v", err, rollbackErr) }
		return fmt.Errorf("commit project transaction: %w", err)
	}
	return nil
}

func (m *Manager) commitLock(lf lockfile.File) error {
	tx, err := m.beginTransaction()
	if err != nil { return err }
	if err := m.SaveLock(m.LockPath(), lf); err != nil {
		_ = m.rollback(tx)
		return fmt.Errorf("save %s: %w", lockfile.FileName, err)
	}
	if err := m.finishTransaction(tx); err != nil {
		_ = m.rollback(tx)
		return err
	}
	return nil
}

func (m *Manager) commitManifest(mf manifest.File) error {
	tx, err := m.beginTransaction()
	if err != nil { return err }
	if err := m.SaveManifest(m.ManifestPath(), mf); err != nil {
		_ = m.rollback(tx)
		return fmt.Errorf("save %s: %w", manifest.FileName, err)
	}
	if err := m.finishTransaction(tx); err != nil {
		_ = m.rollback(tx)
		return err
	}
	return nil
}

func restoreSnapshot(path string, snap fileSnapshot) error {
	if !snap.Exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) { return err }
		return nil
	}
	return writeAtomicRaw(path, snap.Data)
}

func writeAtomicRaw(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(path), ".packrail-project-*.tmp")
	if err != nil { return err }
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil { tmp.Close(); return err }
	if _, err := tmp.Write(data); err != nil { tmp.Close(); return err }
	if err := tmp.Sync(); err != nil { tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	if err := os.Rename(tmpPath, path); err == nil { return nil }
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) { return err }
	return os.Rename(tmpPath, path)
}
