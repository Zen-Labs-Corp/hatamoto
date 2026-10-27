//go:build integration

package pluginhost

import (
	"io/fs"
	"testing/fstest"
)

// fsOf builds an in-memory file system from name -> content.
func fsOf(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}
