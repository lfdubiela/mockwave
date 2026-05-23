package unmatched_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuffer_AddAndList(t *testing.T) {
	b := unmatched.NewBuffer(10)
	b.Add(unmatched.Request{At: time.Now(), Protocol: "http", Method: "GET", Path: "/foo"})
	b.Add(unmatched.Request{At: time.Now(), Protocol: "http", Method: "POST", Path: "/bar"})
	items := b.List()
	require.Len(t, items, 2)
	assert.Equal(t, "/foo", items[0].Path) // oldest first
	assert.Equal(t, "/bar", items[1].Path)
}

func TestBuffer_Clear(t *testing.T) {
	b := unmatched.NewBuffer(10)
	b.Add(unmatched.Request{At: time.Now(), Method: "GET", Path: "/x"})
	b.Clear()
	assert.Empty(t, b.List())
}

func TestBuffer_WrapAround(t *testing.T) {
	b := unmatched.NewBuffer(3)
	for i := range 5 {
		b.Add(unmatched.Request{At: time.Now(), Path: fmt.Sprintf("/%d", i)})
	}
	items := b.List()
	require.Len(t, items, 3)
	// Oldest surviving entry is /2, then /3, then /4
	assert.Equal(t, "/2", items[0].Path)
	assert.Equal(t, "/3", items[1].Path)
	assert.Equal(t, "/4", items[2].Path)
}

func TestBuffer_Empty(t *testing.T) {
	b := unmatched.NewBuffer(10)
	assert.Nil(t, b.List())
}
