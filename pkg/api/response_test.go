package api_test

import (
	"encoding/json"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/api"
)

func TestSuccessResponseMarshalsDataWithStableLowerCamelCaseField(t *testing.T) {
	payload := struct {
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		Empty string `json:"empty,omitempty"`
	}{
		ID:   "plugin_01",
		Slug: "demo-plugin",
	}

	assertJSON(t, api.Success(payload), `{"data":{"id":"plugin_01","slug":"demo-plugin"}}`)
}

func TestErrorResponseMarshalsRequiredFieldsAndOmitsEmptyDetails(t *testing.T) {
	resp := api.NewError("unauthorized", "authentication required", "req_01HZX9E8WQ8P4Z0G3J7V6B2C1D")

	assertJSON(t, resp, `{"code":"unauthorized","message":"authentication required","requestId":"req_01HZX9E8WQ8P4Z0G3J7V6B2C1D"}`)
}

func TestErrorResponseIncludesSafeTextDetailsWhenProvided(t *testing.T) {
	resp := api.NewErrorWithDetails("validation_failed", "invalid request", "req_02HZX9E8WQ8P4Z0G3J7V6B2C1E", "slug is invalid")

	assertJSON(t, resp, `{"code":"validation_failed","message":"invalid request","requestId":"req_02HZX9E8WQ8P4Z0G3J7V6B2C1E","details":"slug is invalid"}`)
}

func TestCursorListResponseMarshalsItemsAndNextCursor(t *testing.T) {
	type item struct {
		Slug string `json:"slug"`
	}

	resp := api.CursorList([]item{{Slug: "alpha"}, {Slug: "bravo"}}, "cursor_next")

	assertJSON(t, resp, `{"items":[{"slug":"alpha"},{"slug":"bravo"}],"nextCursor":"cursor_next"}`)
}

func TestCursorListResponseOmitsEmptyNextCursorAndKeepsItemsAsArray(t *testing.T) {
	type item struct {
		Slug string `json:"slug"`
	}

	resp := api.CursorList[item](nil, "")

	assertJSON(t, resp, `{"items":[]}`)
}

func TestHealthResponseMarshalsMinimalStatusAndOmitsEmptyChecks(t *testing.T) {
	resp := api.NewHealth(api.HealthStatusOK)

	assertJSON(t, resp, `{"status":"ok"}`)
}

func TestHealthResponseMarshalsChecksAndOmitsEmptyCheckMessage(t *testing.T) {
	resp := api.NewHealth(
		api.HealthStatusDegraded,
		api.HealthCheck{Name: "database", Status: api.HealthStatusOK},
		api.HealthCheck{Name: "gitStorage", Status: api.HealthStatusError, Message: "unavailable"},
	)

	assertJSON(t, resp, `{"status":"degraded","checks":[{"name":"database","status":"ok"},{"name":"gitStorage","status":"error","message":"unavailable"}]}`)
}

func assertJSON(t *testing.T, value any, want string) {
	t.Helper()

	got, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("json.Marshal() = %s, want %s", got, want)
	}
}
