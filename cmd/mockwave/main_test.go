package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitProtocols(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"http", []string{"http"}},
		{"http,grpc", []string{"http", "grpc"}},
		{"http, GRPC, GraphQL", []string{"http", "grpc", "graphql"}},
		{"", nil},
		{",,,", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitProtocols(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContainsProtocol(t *testing.T) {
	protocols := []string{"http", "grpc", "graphql"}
	assert.True(t, containsProtocol(protocols, "grpc"))
	assert.True(t, containsProtocol(protocols, "http"))
	assert.False(t, containsProtocol(protocols, "soap"))
	assert.False(t, containsProtocol(nil, "http"))
}

func TestValidateCmd_ValidFile(t *testing.T) {
	dir := t.TempDir()
	cfg := map[string]interface{}{
		"rules": []interface{}{
			map[string]interface{}{"id": "r1"},
			map[string]interface{}{"id": "r2"},
		},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cmd := validateCmd()
	cmd.SetArgs([]string{path})
	err = cmd.Execute()
	require.NoError(t, err)
}

func TestValidateCmd_MissingFile(t *testing.T) {
	cmd := validateCmd()
	cmd.SetArgs([]string{"/nonexistent/file.json"})
	err := cmd.Execute()
	require.Error(t, err)
}

func TestValidateCmd_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))

	cmd := validateCmd()
	cmd.SetArgs([]string{path})
	err := cmd.Execute()
	require.Error(t, err)
}

func TestVersionCmd(t *testing.T) {
	cmd := versionCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)
}

func TestRootCmd_HasSubcommands(t *testing.T) {
	root := rootCmd()
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	assert.True(t, names["start"])
	assert.True(t, names["validate"])
	assert.True(t, names["version"])
}
