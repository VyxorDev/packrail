package project

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProjectLockBlocksAnotherProcess(t *testing.T) {
	const childFlag = "PACKRAIL_PROJECT_LOCK_TEST_CHILD"
	const childPath = "PACKRAIL_PROJECT_LOCK_TEST_PATH"
	if os.Getenv(childFlag) == "1" {
		_, err := acquireProjectLock(os.Getenv(childPath))
		if errors.Is(err, errProjectBusy) {
			os.Exit(0)
		}
		if err != nil {
			t.Fatalf("unexpected child lock error: %v", err)
		}
		t.Fatal("second process unexpectedly acquired the project lock")
	}

	path := filepath.Join(t.TempDir(), "project.lock")
	release, err := acquireProjectLock(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestProjectLockBlocksAnotherProcess$")
	cmd.Env = append(os.Environ(), childFlag+"=1", childPath+"="+path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child process did not observe the lock: %v\n%s", err, output)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	releaseAgain, err := acquireProjectLock(path)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	if err := releaseAgain(); err != nil {
		t.Fatal(err)
	}
}
