package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStore_JSON(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cfg.json")
	require.NoError(t, os.WriteFile(f, []byte(`{"rules":[],"simulations":[]}`), 0o600))
	s, err := buildStore("json", f, storeOpts{})
	require.NoError(t, err)
	assert.NotNil(t, s)
}

func TestBuildStore_JSON_MissingFile(t *testing.T) {
	_, err := buildStore("json", "/nonexistent/file.json", storeOpts{})
	require.Error(t, err)
}

func TestBuildStore_UnknownType(t *testing.T) {
	_, err := buildStore("redis", "", storeOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown store")
}
