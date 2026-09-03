package utils

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The fork carries the pooled-resource synchronization fix that streaming
// cancellation depends on (see SetupStreamCancellation and
// TestStreamCloseUnderActiveReaderIsSafe). A module that requires upstream
// fasthttp without the replace builds against v1.71.0-v1.73.0, where closing a
// stream under an active reader double-releases the pooled requestStream.
const (
	upstreamFasthttp = "github.com/valyala/fasthttp"
	forkedFasthttp   = "github.com/maximhq/fasthttp"
)

var (
	requireFasthttpRe = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(upstreamFasthttp) + `\s+v\S+`)
	replaceFasthttpRe = regexp.MustCompile(`(?m)^\s*replace\s+` + regexp.QuoteMeta(upstreamFasthttp) +
		`\s+=>\s+` + regexp.QuoteMeta(forkedFasthttp) + `\s+v\S+`)
)

// TestFasthttpReplaceIsConsistentAcrossModules guards the invariant the comment
// above core/go.mod's replace directive documents: Go ignores replace directives
// from non-main modules, so every module in this workspace that requires
// upstream fasthttp has to carry the replace itself. A new module added without
// one compiles fine and fails only under load, so pin it here instead.
//
// Only the presence of the replace is asserted. Disagreeing fork versions are
// already fatal at load time ("conflicting replacements for
// github.com/valyala/fasthttp"), so checking them here would be dead code, and
// pinning the version would just make every deliberate bump edit this file.
func TestFasthttpReplaceIsConsistentAcrossModules(t *testing.T) {
	root := repoRoot(t)

	var replaced, missing []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !requireFasthttpRe.Match(content) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !replaceFasthttpRe.Match(content) {
			missing = append(missing, rel)
			return nil
		}
		replaced = append(replaced, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking modules: %v", err)
	}

	if len(replaced) == 0 && len(missing) == 0 {
		t.Fatalf("no go.mod requiring %s found under %s - the guard is not looking where it thinks", upstreamFasthttp, root)
	}
	if len(missing) > 0 {
		t.Errorf("these modules require %s without replacing it with %s, so they build against\n"+
			"upstream fasthttp and lose the pooled-resource fix:\n  %s",
			upstreamFasthttp, forkedFasthttp, strings.Join(missing, "\n  "))
	}
}

// repoRoot walks up from this package to the directory holding go.work, which is
// the workspace root every module in this repo lives under.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.work found above %q", dir)
		}
		dir = parent
	}
}
