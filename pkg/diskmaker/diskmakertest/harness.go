package diskmakertest

import (
	"os"
	"testing"

	"github.com/openshift/local-storage-operator/pkg/internal"
)

// TempDir creates a temporary directory with the given prefix and registers t.Cleanup
// to remove it. Use instead of ad-hoc createTmpDir/createTempDir in controller tests.
func TempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

// WithInternalMocks snapshots internal.FilePathGlob, FilePathEvalSymLinks, CmdExecutor, and
// Readlink, runs setup (which should assign test doubles), then registers t.Cleanup to
// restore originals. Do not nest overlapping calls in the same subtest without restoring
// between them.
func WithInternalMocks(t *testing.T, setup func()) {
	t.Helper()
	origGlob := internal.FilePathGlob
	origEval := internal.FilePathEvalSymLinks
	origExec := internal.CmdExecutor
	origReadlink := internal.Readlink
	t.Cleanup(func() {
		internal.FilePathGlob = origGlob
		internal.FilePathEvalSymLinks = origEval
		internal.CmdExecutor = origExec
		internal.Readlink = origReadlink
	})
	setup()
}
