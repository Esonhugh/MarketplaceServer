package distribution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/google/uuid"
	"github.com/juanjiTech/jin"
)

func TestMarketplaceJSONServesStoredBytesAndConditionalRequests(t *testing.T) {
	id := uuid.New()
	publicKey, err := distributionservice.ParseMarketplacePublicKey("ctf-web-a3f91c20")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"name":"ctf-web","owner":{"name":"Security"},"plugins":[]}`)
	resolver := &fakeResolver{marketplace: distributionservice.MarketplaceGrant{
		DistributionID: id, PublicKey: publicKey, RevisionID: uuid.New(), ContentJSON: content,
		ContentDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}}
	engine := jin.New()
	NewMarketplaceJSONHandler(resolver).Register(engine)

	target := "/distribution/marketplaces/" + publicKey.String() + "/marketplace.json"
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != string(content) {
		t.Fatalf("GET status/body = %d/%q", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	etag := recorder.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag is empty")
	}

	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("If-None-Match", etag)
	conditional := httptest.NewRecorder()
	engine.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional status/body = %d/%q", conditional.Code, conditional.Body.String())
	}
}

func TestMarketplaceJSONRejectsNonCanonicalOrUnknownPublicKey(t *testing.T) {
	resolver := &fakeResolver{err: distributionservice.ErrNotFound}
	engine := jin.New()
	NewMarketplaceJSONHandler(resolver).Register(engine)
	for _, id := range []string{"not-a-key", "CTF-web-a3f91c20", uuid.NewString(), "unknown-a3f91c20"} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/distribution/marketplaces/"+id+"/marketplace.json", nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET ID %q status = %d, want 404", id, recorder.Code)
		}
	}
	if resolver.marketplaceCalls != 1 {
		t.Fatalf("resolver calls = %d, want only canonical unknown key", resolver.marketplaceCalls)
	}
}

type fakeResolver struct {
	marketplace      distributionservice.MarketplaceGrant
	err              error
	marketplaceCalls int
}

func (resolver *fakeResolver) ResolvePlugin(context.Context, uuid.UUID) (distributionservice.PluginGrant, error) {
	return distributionservice.PluginGrant{}, resolver.err
}
func (resolver *fakeResolver) ResolveMarketplace(context.Context, distributionservice.MarketplacePublicKey) (distributionservice.MarketplaceGrant, error) {
	resolver.marketplaceCalls++
	return resolver.marketplace, resolver.err
}
