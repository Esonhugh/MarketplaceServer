package git

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"github.com/juanjiTech/jin"
)

func TestPublicDistributionRoutesAllowOnlyUploadPack(t *testing.T) {
	id := uuid.New()
	projection := gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindPlugin, StorageKey: uuid.NewString()}
	resolver := &routeDistributionResolver{plugin: distributionservice.PluginGrant{DistributionID: id, Projection: projection}}
	reader := &recordingDistributionReader{}
	engine := jin.New()
	mod := &Mod{distributionResolver: resolver, distributionReader: reader, config: Config{maxRequestBytes: 1024}}
	mod.registerDistributionRoutes(engine)

	base := "/distribution/plugins/" + id.String() + ".git"
	advertisement := httptest.NewRecorder()
	engine.ServeHTTP(advertisement, httptest.NewRequest(http.MethodGet, base+"/info/refs?service=git-upload-pack", nil))
	if advertisement.Code != http.StatusOK || reader.advertiseCalls != 1 {
		t.Fatalf("advertisement status/calls = %d/%d", advertisement.Code, reader.advertiseCalls)
	}
	result := httptest.NewRecorder()
	engine.ServeHTTP(result, httptest.NewRequest(http.MethodPost, base+"/git-upload-pack", strings.NewReader("request")))
	if result.Code != http.StatusOK || reader.uploadCalls != 1 {
		t.Fatalf("upload status/calls = %d/%d", result.Code, reader.uploadCalls)
	}
	for _, target := range []string{
		base + "/info/refs?service=git-receive-pack", base + "/git-receive-pack", base + "/HEAD", base + "/objects/info/packs",
	} {
		recorder := httptest.NewRecorder()
		method := http.MethodGet
		if strings.Contains(target, "receive-pack") && !strings.Contains(target, "info/refs") {
			method = http.MethodPost
		}
		engine.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", method, target, recorder.Code)
		}
	}
	if reader.advertiseCalls != 1 || reader.uploadCalls != 1 {
		t.Fatalf("rejected requests reached reader: advertise=%d upload=%d", reader.advertiseCalls, reader.uploadCalls)
	}
}

func TestPublicMarketplaceDistributionGitRoutesResolveOneProjectionPerRequest(t *testing.T) {
	id := uuid.New()
	publicKey, err := distributionservice.ParseMarketplacePublicKey("ctf-web-a3f91c20")
	if err != nil {
		t.Fatal(err)
	}
	projection := gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindMarketplace, StorageKey: uuid.NewString()}
	resolver := &routeDistributionResolver{marketplace: distributionservice.MarketplaceGrant{DistributionID: id, PublicKey: publicKey, RevisionID: uuid.New(), Projection: projection}}
	reader := &recordingDistributionReader{}
	engine := jin.New()
	mod := &Mod{distributionResolver: resolver, distributionReader: reader}
	mod.registerDistributionRoutes(engine)

	target := "/distribution/marketplaces/" + publicKey.String() + ".git/info/refs?service=git-upload-pack"
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK || resolver.marketplaceCalls != 1 || reader.advertiseCalls != 1 {
		t.Fatalf("status/resolver/reader = %d/%d/%d", recorder.Code, resolver.marketplaceCalls, reader.advertiseCalls)
	}
}

func TestPluginDistributionRoutesRejectMalformedUUIDBeforeResolution(t *testing.T) {
	resolver := &routeDistributionResolver{}
	reader := &recordingDistributionReader{}
	engine := jin.New()
	mod := &Mod{distributionResolver: resolver, distributionReader: reader}
	mod.registerDistributionRoutes(engine)
	for _, id := range []string{"not-a-uuid", "550E8400-E29B-41D4-A716-446655440000", uuid.NewString() + ".git"} {
		recorder := httptest.NewRecorder()
		target := "/distribution/plugins/" + id + ".git/info/refs?service=git-upload-pack"
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("malformed ID %q status = %d", id, recorder.Code)
		}
	}
	if resolver.pluginCalls != 0 {
		t.Fatalf("malformed IDs reached resolver %d times", resolver.pluginCalls)
	}
}

func TestDistributionUploadRejectsKnownOversizeBeforeResolution(t *testing.T) {
	resolver := &routeDistributionResolver{}
	reader := &recordingDistributionReader{}
	engine := jin.New()
	mod := &Mod{distributionResolver: resolver, distributionReader: reader, config: Config{maxRequestBytes: 4}}
	mod.registerDistributionRoutes(engine)

	request := httptest.NewRequest(http.MethodPost, "/distribution/marketplaces/ctf-web-a3f91c20.git/git-upload-pack", strings.NewReader("oversized"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status = %d, want 413", recorder.Code)
	}
	if resolver.marketplaceCalls != 0 || reader.uploadCalls != 0 {
		t.Fatalf("oversized request reached resolver/reader = %d/%d", resolver.marketplaceCalls, reader.uploadCalls)
	}
}

func TestMarketplaceDistributionRoutesRejectMalformedPublicKeyBeforeResolution(t *testing.T) {
	resolver := &routeDistributionResolver{}
	reader := &recordingDistributionReader{}
	engine := jin.New()
	mod := &Mod{distributionResolver: resolver, distributionReader: reader}
	mod.registerDistributionRoutes(engine)
	for _, publicKey := range []string{"not-a-key", "CTF-web-a3f91c20", uuid.NewString(), "web-a3f91c2", "web-a3f91c200"} {
		recorder := httptest.NewRecorder()
		target := "/distribution/marketplaces/" + publicKey + ".git/info/refs?service=git-upload-pack"
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("malformed public key %q status = %d", publicKey, recorder.Code)
		}
	}
	if resolver.marketplaceCalls != 0 {
		t.Fatalf("malformed public keys reached resolver %d times", resolver.marketplaceCalls)
	}
}

type routeDistributionResolver struct {
	plugin           distributionservice.PluginGrant
	marketplace      distributionservice.MarketplaceGrant
	err              error
	pluginCalls      int
	marketplaceCalls int
}

func (resolver *routeDistributionResolver) ResolvePlugin(context.Context, uuid.UUID) (distributionservice.PluginGrant, error) {
	resolver.pluginCalls++
	return resolver.plugin, resolver.err
}
func (resolver *routeDistributionResolver) ResolveMarketplace(context.Context, distributionservice.MarketplacePublicKey) (distributionservice.MarketplaceGrant, error) {
	resolver.marketplaceCalls++
	return resolver.marketplace, resolver.err
}

type recordingDistributionReader struct {
	advertiseCalls int
	uploadCalls    int
}

func (reader *recordingDistributionReader) AdvertiseDistribution(_ context.Context, _ gitservice.ImmutableProjection, stdout, _ io.Writer) error {
	reader.advertiseCalls++
	_, _ = io.Copy(stdout, bytes.NewBufferString("advertisement"))
	return nil
}
func (reader *recordingDistributionReader) UploadDistribution(_ context.Context, _ gitservice.ImmutableProjection, _ io.Reader, stdout, _ io.Writer) error {
	reader.uploadCalls++
	_, _ = io.Copy(stdout, bytes.NewBufferString("result"))
	return nil
}
