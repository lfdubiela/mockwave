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

func TestBuffer_ListDeduped_CollapsesSamePathAndBody(t *testing.T) {
	b := unmatched.NewBuffer(10)
	b.Add(unmatched.Request{At: time.Unix(1, 0), Method: "GET", Path: "/a", Body: "x"})
	b.Add(unmatched.Request{At: time.Unix(2, 0), Method: "GET", Path: "/b", Body: "x"}) // diff path
	b.Add(unmatched.Request{At: time.Unix(3, 0), Method: "POST", Path: "/a", Body: "x"}) // dup path+body (method differs, still dup)
	b.Add(unmatched.Request{At: time.Unix(4, 0), Method: "GET", Path: "/a", Body: "y"})  // same path, diff body

	items := b.ListDeduped()
	require.Len(t, items, 3) // (/a,x) collapsed to one; (/b,x); (/a,y)

	// (/a,x) keeps the most recent occurrence (At=3, method POST).
	var ax *unmatched.Request
	for i := range items {
		if items[i].Path == "/a" && items[i].Body == "x" {
			ax = &items[i]
		}
	}
	require.NotNil(t, ax)
	assert.Equal(t, "POST", ax.Method)
	assert.Equal(t, int64(3), ax.At.Unix())
	assert.Equal(t, 2, ax.Count) // two (/a,x) calls collapsed

	// Oldest-first ordering preserved among kept entries.
	assert.True(t, items[0].At.Before(items[1].At))
	assert.True(t, items[1].At.Before(items[2].At))
}

func TestBuffer_ListDeduped_Empty(t *testing.T) {
	b := unmatched.NewBuffer(5)
	assert.Empty(t, b.ListDeduped())
}
