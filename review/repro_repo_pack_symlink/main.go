// Run with: go run ./review/repro_repo_pack_symlink
// Uses a disposable local file as the out-of-workspace target.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tjohnson/maestro/internal/config"
)

func main() {
	root, err := os.MkdirTemp("", "maestro-pack-symlink-")
	check(err)
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	check(os.MkdirAll(workspace, 0o700))
	check(os.MkdirAll(outside, 0o700))
	check(os.WriteFile(filepath.Join(outside, "prompt.md"), []byte("OUTSIDE_FIXTURE_MARKER\n"), 0o600))
	check(os.Symlink(outside, filepath.Join(workspace, ".maestro")))
	_, err = config.ResolveRepoPack(workspace, ".maestro")
	if err == nil {
		panic("repository pack symlink was accepted")
	}
	fmt.Printf("repo_pack_symlink_rejected=true\n")
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
