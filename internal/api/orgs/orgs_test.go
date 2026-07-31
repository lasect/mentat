package orgs

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreorgs "mentat/internal/orgs"
)

func TestDecodeJSON(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Acme"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(response, request, &input) || input.Name != "Acme" {
		t.Fatalf("decode failed: %s", response.Body.String())
	}
}

func TestDecodeJSONRejectsNonJSONMediaType(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Acme"}`))
	request.Header.Set("Content-Type", "application/jsonp")
	response := httptest.NewRecorder()
	var input struct {
		Name string `json:"name"`
	}
	if decodeJSON(response, request, &input) {
		t.Fatal("decodeJSON accepted a non-JSON media type")
	}
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestOrganizationErrorResponses(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{coreorgs.ErrNotFound, http.StatusNotFound, "not_found"},
		{coreorgs.ErrForbidden, http.StatusForbidden, "forbidden"},
		{coreorgs.ErrPaidPlanRequired, http.StatusPaymentRequired, "paid_plan_required"},
		{coreorgs.ErrInvalidConnection, http.StatusUnprocessableEntity, "invalid_connection_string"},
		{errors.New("database unavailable"), http.StatusInternalServerError, "internal_error"},
	} {
		response := httptest.NewRecorder()
		writeServiceError(response, test.err)
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"`+test.code+`"`) {
			t.Fatalf("writeServiceError(%v) = %d %s", test.err, response.Code, response.Body.String())
		}
	}
}

func TestNewHandlerRequiresService(t *testing.T) {
	if _, err := NewHandler(nil); err == nil {
		t.Fatal("NewHandler(nil) succeeded")
	}
}
