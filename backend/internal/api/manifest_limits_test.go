package api_test

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
)

func TestStandaloneManifestClientCeilingAndStoredOversize(t *testing.T) {
	env := setupAuthFixture(t, false) // Deliberately configured above the client ceiling.
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	path := "/transfers/" + id
	max := bytes.Repeat([]byte{1}, database.MaxManifestBytes)
	rawAuthorized(t, env, "POST", path+"/manifest", env.userToken, append(max, 1), 413)
	rawAuthorized(t, env, "POST", path+"/manifest", env.userToken, max, 204)
	authRequest(t, env, "POST", path+"/complete", env.userToken, nil, 204)
	if got := rawAuthorized(t, env, "GET", path+"/manifest", "", nil, 200); !bytes.Equal(got, max) {
		t.Fatalf("manifest changed: received %d bytes", len(got))
	}
	// Emulate a historical database row created before the protocol ceiling.
	if err := env.queries.SaveManifest(id, append(max, 1)); err != nil {
		t.Fatal(err)
	}
	if data, err := env.queries.GetManifest(id); !errors.Is(err, database.ErrManifestTooLarge) || data != nil {
		t.Fatalf("oversized database read returned %d bytes, %v", len(data), err)
	}
	authRequest(t, env, "GET", path, "", nil, 200)
	got := authRequest(t, env, "GET", path+"/manifest", "", nil, 413)
	if got["code"] != "unsupported_manifest" {
		t.Fatal(got)
	}
}

func TestManifestOperatorMayLowerCeiling(t *testing.T) {
	env := setupAuthFixture(t, false)
	server := api.NewServer(config.Config{
		AuthAllowInsecureHTTP: true, DefaultExpiry: time.Hour,
		MaxManifestSize: 128, RateLimitGlobal: 1000, RateLimitCreation: 1000,
		RateLimitBurst: 2000, RateLimitCreationBurst: 2000,
	}, env.queries, fixtureStore(t, env))
	env.server = httptest.NewServer(server.Router())
	t.Cleanup(env.server.Close)
	id := authRequest(t, env, "POST", "/transfers", env.userToken, nil, 201)["id"].(string)
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", env.userToken, make([]byte, 129), 413)
	rawAuthorized(t, env, "POST", "/transfers/"+id+"/manifest", env.userToken, make([]byte, 128), 204)
}
