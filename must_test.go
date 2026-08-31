package httpassert

import (
	"errors"
	"net/http"
	"testing"
)

func TestMust(t *testing.T) {
	t.Parallel()

	t.Run("returns a successfully constructed assertion", func(t *testing.T) {
		assertion := Must(AssertJQ(`.status == "healthy"`))
		if assertion == nil || assertion.Kind() != KindJQ {
			t.Errorf("Must() = %v, want jq assertion", assertion)
		}
	})

	t.Run("returns an HTTP request", func(t *testing.T) {
		request := Must(http.NewRequest(http.MethodGet, "https://example.test/health", nil))
		if request.Method != http.MethodGet || request.URL.String() != "https://example.test/health" {
			t.Errorf("Must() = %s %s, want GET https://example.test/health", request.Method, request.URL)
		}
	})

	t.Run("panics with the constructor error", func(t *testing.T) {
		want := errors.New("invalid value")
		defer func() {
			if got := recover(); got != want {
				t.Errorf("panic = %v, want %v", got, want)
			}
		}()

		Must("", want)
	})
}
