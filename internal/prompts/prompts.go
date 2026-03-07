package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
)

//go:embed *.md
var promptFiles embed.FS

var (
	cache    map[string]string
	initOnce sync.Once
	initErr  error
)

// load walks the embedded *.md files once and populates the cache.
func load() {
	cache = make(map[string]string)
	initErr = fs.WalkDir(promptFiles, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		content, err := promptFiles.ReadFile(path)
		if err != nil {
			return err
		}
		key := filepath.Base(path)
		key = key[:len(key)-len(filepath.Ext(key))]
		cache[key] = string(content)
		return nil
	})
}

// MustGet returns the prompt content for the given name (filename without .md).
// It panics if the prompt does not exist or the embedded FS failed to load.
func MustGet(name string) string {
	initOnce.Do(load)
	if initErr != nil {
		panic(fmt.Sprintf("prompts: failed to load embedded prompts: %v", initErr))
	}
	val, ok := cache[name]
	if !ok {
		panic(fmt.Sprintf("prompts: %q not found (available: %v)", name, keys()))
	}
	return val
}

func keys() []string {
	out := make([]string, 0, len(cache))
	for k := range cache {
		out = append(out, k)
	}
	return out
}
