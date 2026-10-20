package supportchannels

import (
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

func TestRegisterBuildsOpenAPISchemas(t *testing.T) {
	t.Parallel()
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("support channels test", "0"))
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}

	(&Service{registry: registry}).register(api, router)
}
