package distribution

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/juanjiTech/jin"
)

type MarketplaceJSONHandler struct {
	resolver distributionservice.Resolver
}

func NewMarketplaceJSONHandler(resolver distributionservice.Resolver) *MarketplaceJSONHandler {
	return &MarketplaceJSONHandler{resolver: resolver}
}

func (handler *MarketplaceJSONHandler) Register(engine *jin.Engine) {
	engine.GET("/distribution/marketplaces/:distribution/marketplace.json", handler.Get)
}

func (handler *MarketplaceJSONHandler) Get(c *jin.Context) {
	publicKey, err := distributionservice.ParseMarketplacePublicKey(c.Params.ByName("distribution"))
	if err != nil {
		http.NotFound(c.Writer, c.Request)
		return
	}
	grant, err := handler.resolver.ResolveMarketplace(c.Request.Context(), publicKey)
	if err != nil || grant.ContentDigest == "" {
		http.NotFound(c.Writer, c.Request)
		return
	}
	etag := `"` + grant.ContentDigest + `"`
	header := c.Writer.Header()
	header.Set("Content-Type", "application/json")
	header.Set("Cache-Control", "no-cache")
	header.Set("ETag", etag)
	header.Set("X-Content-Type-Options", "nosniff")
	if etagMatches(c.Request.Header.Get("If-None-Match"), etag) {
		c.Writer.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Length", strconv.Itoa(len(grant.ContentJSON)))
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(grant.ContentJSON)
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || len(candidate) == len(etag) && subtle.ConstantTimeCompare([]byte(candidate), []byte(etag)) == 1 {
			return true
		}
	}
	return false
}
