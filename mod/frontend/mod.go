package frontend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/juanjiTech/jin"
)

var _ kernel.Module = (*Mod)(nil)

//go:embed all:dist
var embeddedDist embed.FS

// Config controls the embedded static frontend.
type Config struct {
	Disabled bool   `yaml:"disabled" mapstructure:"disabled"`
	BasePath string `yaml:"basePath" mapstructure:"basePath"`
}

// Mod serves the compiled Svelte SPA from the embedded dist directory.
type Mod struct {
	kernel.UnimplementedModule
	config Config
}

func (m *Mod) Name() string { return "frontend" }

func (m *Mod) Config() any { return &m.config }

func (m *Mod) Load(hub *kernel.Hub) error {
	if m.config.Disabled {
		return nil
	}

	var engine *jin.Engine
	if err := hub.Load(&engine); err != nil {
		return errors.New("can't load jin.Engine from kernel")
	}

	basePath, err := normalizeBasePath(m.config.BasePath)
	if err != nil {
		return err
	}

	dist, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		return fmt.Errorf("load embedded frontend dist: %w", err)
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return fmt.Errorf("embedded frontend dist missing index.html: %w", err)
	}

	server := &staticServer{fsys: dist, basePath: basePath}
	engine.NoRoute(server.handle)
	return nil
}

func (m *Mod) Stop(wg *sync.WaitGroup, _ context.Context) error {
	defer wg.Done()
	return nil
}

type staticServer struct {
	fsys     fs.FS
	basePath string
}

var hashedAssetPattern = regexp.MustCompile(`(?i)(^|[._-])[a-z0-9_-]{8,}(\.|$)`)

func (s *staticServer) handle(c *jin.Context) {
	s.serve(c.Writer, c.Request)
}

func (s *staticServer) serve(w http.ResponseWriter, r *http.Request) {
	requestPath, ok := s.pathWithinBase(r.URL.Path)
	if !ok {
		notFound(w, r)
		return
	}
	if isReservedPath(requestPath) {
		notFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	filePath, ok := cleanRequestPath(r.URL)
	if !ok {
		notFound(w, r)
		return
	}
	filePath, ok = stripCleanBasePath(filePath, s.basePath)
	if !ok {
		notFound(w, r)
		return
	}

	if filePath != "" {
		if s.serveStaticFile(w, r, filePath, false) {
			return
		}
	}

	if filepath.Ext(filePath) != "" {
		notFound(w, r)
		return
	}

	s.serveStaticFile(w, r, "index.html", true)
}

func (s *staticServer) serveStaticFile(w http.ResponseWriter, r *http.Request, name string, index bool) bool {
	if !fs.ValidPath(name) {
		return false
	}

	file, err := s.fsys.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	data, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		return false
	}
	if index && s.basePath != "/" {
		data = scopeIndexAssets(data, s.basePath)
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	setCacheHeaders(w.Header(), name, index)
	setETag(w.Header(), data)
	http.ServeContent(w, r, name, modTime(info), bytes.NewReader(data))
	return true
}

func normalizeBasePath(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("frontend basePath must start with /: %q", raw)
	}
	if strings.Contains(raw, "\x00") {
		return "", errors.New("frontend basePath contains NUL byte")
	}
	if hasDotDotSegment(raw) {
		return "", fmt.Errorf("frontend basePath must not contain path traversal: %q", raw)
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == "/" {
		return "/", nil
	}
	return strings.TrimRight(cleaned, "/"), nil
}

func (s *staticServer) pathWithinBase(requestPath string) (string, bool) {
	if s.basePath == "/" {
		return requestPath, true
	}
	if requestPath == s.basePath {
		return "/", true
	}
	prefix := s.basePath + "/"
	if strings.HasPrefix(requestPath, prefix) {
		return "/" + strings.TrimPrefix(requestPath, prefix), true
	}
	return "", false
}

func stripCleanBasePath(cleanPath, basePath string) (string, bool) {
	if basePath == "/" {
		return cleanPath, true
	}
	base := strings.TrimPrefix(basePath, "/")
	if cleanPath == base {
		return "", true
	}
	prefix := base + "/"
	if strings.HasPrefix(cleanPath, prefix) {
		return strings.TrimPrefix(cleanPath, prefix), true
	}
	return "", false
}

func cleanRequestPath(u *url.URL) (string, bool) {
	escaped := u.EscapedPath()
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", false
	}
	if decoded == "" {
		decoded = "/"
	}
	if strings.Contains(decoded, "\x00") || strings.Contains(decoded, "//") || hasDotDotSegment(decoded) {
		return "", false
	}
	cleaned := path.Clean("/" + decoded)
	if cleaned == "/" {
		return "", true
	}
	return strings.TrimPrefix(cleaned, "/"), true
}

func hasDotDotSegment(p string) bool {
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func isReservedPath(p string) bool {
	cleaned := path.Clean("/" + p)
	reserved := []string{
		"/api",
		"/api/v1",
		"/gapi",
		"/git",
		"/distribution",
		"/marketplaces",
		"/healthz",
		"/debug",
		"/metrics",
	}
	for _, prefix := range reserved {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return true
		}
	}
	return false
}

func scopeIndexAssets(data []byte, basePath string) []byte {
	data = bytes.ReplaceAll(data, []byte(`src="/`), []byte(`src="`+basePath+`/`))
	return bytes.ReplaceAll(data, []byte(`href="/`), []byte(`href="`+basePath+`/`))
}

func setCacheHeaders(header http.Header, name string, index bool) {
	if index || name == "index.html" {
		header.Set("Cache-Control", "no-cache")
		return
	}
	if hashedAssetPattern.MatchString(path.Base(name)) {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	header.Set("Cache-Control", "no-cache")
}

func setETag(header http.Header, data []byte) {
	sum := sha256.Sum256(data)
	header.Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
}

func modTime(info fs.FileInfo) time.Time {
	if t := info.ModTime(); !t.IsZero() {
		return t
	}
	return time.Unix(0, 0).UTC()
}

func notFound(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}
