package main

import (
	"github.com/dop251/goja"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

var _ = cobra.Command{}
var _ = goja.Runtime{}
var _ = fsnotify.NewWatcher
var _ = assert.NoError

func main() {
}
