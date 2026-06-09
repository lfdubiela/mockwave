package restapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/stretchr/testify/require"
)

func newUIServer(t *testing.T) http.Handler {
	t.Helper()
	return restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
}

func getIndex(t *testing.T, h http.Handler, cookie string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "mw-theme", Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}

func TestThemeInjection_DefaultMacos(t *testing.T) {
	body := getIndex(t, newUIServer(t), "")
	require.Contains(t, body, `data-theme="macos"`)
}

func TestThemeInjection_AmberCookie(t *testing.T) {
	body := getIndex(t, newUIServer(t), "amber")
	require.Contains(t, body, `data-theme="amber"`)
}

func TestThemeInjection_InvalidCookieFallsBackToMacos(t *testing.T) {
	body := getIndex(t, newUIServer(t), "bogus")
	require.Contains(t, body, `data-theme="macos"`)
	require.NotContains(t, body, `data-theme="bogus"`)
}
