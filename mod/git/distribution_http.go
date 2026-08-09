package git

import (
	"io"
	"net/http"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	jinengine "github.com/juanjiTech/jin"
)

func (m *Mod) registerDistributionRoutes(engine *jinengine.Engine) {
	engine.GET("/distribution/marketplaces/:distribution/info/refs", m.handleMarketplaceDistributionInfoRefs)
	engine.POST("/distribution/marketplaces/:distribution/git-upload-pack", m.handleMarketplaceDistributionUploadPack)
	engine.GET("/distribution/plugins/:distribution/info/refs", m.handlePluginDistributionInfoRefs)
	engine.POST("/distribution/plugins/:distribution/git-upload-pack", m.handlePluginDistributionUploadPack)
}

func (m *Mod) handleMarketplaceDistributionInfoRefs(c *jinengine.Context) {
	m.handleDistributionInfoRefs(c, gitservice.ProjectionKindMarketplace)
}

func (m *Mod) handlePluginDistributionInfoRefs(c *jinengine.Context) {
	m.handleDistributionInfoRefs(c, gitservice.ProjectionKindPlugin)
}

func (m *Mod) handleMarketplaceDistributionUploadPack(c *jinengine.Context) {
	m.handleDistributionUploadPack(c, gitservice.ProjectionKindMarketplace)
}

func (m *Mod) handlePluginDistributionUploadPack(c *jinengine.Context) {
	m.handleDistributionUploadPack(c, gitservice.ProjectionKindPlugin)
}

func (m *Mod) handleDistributionInfoRefs(c *jinengine.Context, kind gitservice.ProjectionKind) {
	if c.Request.URL.Query().Get("service") != "git-upload-pack" {
		writePlain(c, http.StatusNotFound, "distribution not found\n")
		return
	}
	projection, ok := m.resolveDistribution(c, kind)
	if !ok {
		writePlain(c, http.StatusNotFound, "distribution not found\n")
		return
	}
	setGitHeaders(c, "application/x-git-upload-pack-advertisement")
	if err := m.distributionReader.AdvertiseDistribution(c.Request.Context(), projection, c.Writer, io.Discard); err != nil {
		m.handleGitError(c, err)
	}
}

func (m *Mod) handleDistributionUploadPack(c *jinengine.Context, kind gitservice.ProjectionKind) {
	if !contentLengthWithinLimit(c, m.config.maxRequestBytes) {
		return
	}
	projection, ok := m.resolveDistribution(c, kind)
	if !ok {
		writePlain(c, http.StatusNotFound, "distribution not found\n")
		return
	}
	setGitHeaders(c, "application/x-git-upload-pack-result")
	body := c.Request.Body
	if m.config.maxRequestBytes > 0 {
		body = http.MaxBytesReader(c.Writer, c.Request.Body, m.config.maxRequestBytes)
	}
	if err := m.distributionReader.UploadDistribution(c.Request.Context(), projection, body, c.Writer, io.Discard); err != nil {
		m.handleGitError(c, err)
	}
}

func (m *Mod) resolveDistribution(c *jinengine.Context, kind gitservice.ProjectionKind) (gitservice.ImmutableProjection, bool) {
	raw := c.Params.ByName("distribution")
	if !strings.HasSuffix(raw, ".git") {
		return gitservice.ImmutableProjection{}, false
	}
	raw = strings.TrimSuffix(raw, ".git")
	var projection gitservice.ImmutableProjection
	switch kind {
	case gitservice.ProjectionKindMarketplace:
		publicKey, err := distributionservice.ParseMarketplacePublicKey(raw)
		if err != nil {
			return gitservice.ImmutableProjection{}, false
		}
		grant, err := m.distributionResolver.ResolveMarketplace(c.Request.Context(), publicKey)
		if err != nil {
			return gitservice.ImmutableProjection{}, false
		}
		projection = grant.Projection
	case gitservice.ProjectionKindPlugin:
		id, err := uuid.Parse(raw)
		if err != nil || id.String() != raw {
			return gitservice.ImmutableProjection{}, false
		}
		grant, err := m.distributionResolver.ResolvePlugin(c.Request.Context(), id)
		if err != nil {
			return gitservice.ImmutableProjection{}, false
		}
		projection = grant.Projection
	default:
		return gitservice.ImmutableProjection{}, false
	}
	if projection.Kind != kind {
		return gitservice.ImmutableProjection{}, false
	}
	return projection, true
}
