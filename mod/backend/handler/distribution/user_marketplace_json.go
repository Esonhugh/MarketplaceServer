package distribution

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/juanjiTech/jin"
)

// UserMarketplaceJSONHandler is deliberately unregistered here. Backend route
// wiring is owned by the concurrent backend task.
type UserMarketplaceJSONHandler struct {
	authenticator auth.BasicAuthenticator
	service       *distributiondomain.UserMarketplaceService
}

func NewUserMarketplaceJSONHandler(authenticator auth.BasicAuthenticator, service *distributiondomain.UserMarketplaceService) *UserMarketplaceJSONHandler {
	return &UserMarketplaceJSONHandler{authenticator: authenticator, service: service}
}

func (handler *UserMarketplaceJSONHandler) Register(engine *jin.Engine) {
	engine.GET("/distribution/users/:username/marketplace.json", handler.Get)
}

func (handler *UserMarketplaceJSONHandler) Get(c *jin.Context) {
	username := c.Params.ByName("username")
	request := c.Request
	providedUsername, password, ok := request.BasicAuth()
	if !ok || handler.authenticator == nil || handler.service == nil || !validUsername(username) || subtle.ConstantTimeCompare([]byte(username), []byte(providedUsername)) != 1 {
		http.NotFound(c.Writer, request)
		return
	}
	principal, err := handler.authenticator.AuthenticateBasic(request.Context(), providedUsername, password)
	if err != nil || !principal.IsUser() || subtle.ConstantTimeCompare([]byte(username), []byte(principal.Username())) != 1 {
		http.NotFound(c.Writer, request)
		return
	}
	origin, err := requestOrigin(request)
	if err != nil {
		http.NotFound(c.Writer, request)
		return
	}
	result, err := handler.service.Render(request.Context(), principal, username, origin)
	if err != nil {
		http.NotFound(c.Writer, request)
		return
	}

	header := c.Writer.Header()
	header.Set("Content-Type", "application/json")
	header.Set("Cache-Control", "private, no-store")
	header.Set("Pragma", "no-cache")
	header.Set("ETag", result.ETag)
	header.Set("Vary", "Authorization, Host")
	header.Set("X-Content-Type-Options", "nosniff")
	if userETagMatches(request.Header.Get("If-None-Match"), result.ETag) {
		c.Writer.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Length", strconv.Itoa(len(result.ContentJSON)))
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(result.ContentJSON)
}

func validUsername(username string) bool {
	for _, runeValue := range username {
		if !(runeValue >= 'a' && runeValue <= 'z' || runeValue >= '0' && runeValue <= '9' || runeValue == '-') {
			return false
		}
	}
	return username != "" && !strings.HasPrefix(username, "-") && !strings.HasSuffix(username, "-") && !strings.Contains(username, "--")
}

func requestOrigin(request *http.Request) (string, error) {
	if request == nil || request.Host == "" || strings.ContainsAny(request.Host, "@/\\") || strings.TrimSpace(request.Host) != request.Host {
		return "", errors.New("invalid request host")
	}
	host, err := canonicalHost(request.Host)
	if err != nil {
		return "", err
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host, nil
}

func canonicalHost(raw string) (string, error) {
	host, port := raw, ""
	if strings.HasPrefix(raw, "[") {
		parsedHost, parsedPort, err := net.SplitHostPort(raw)
		if err != nil || net.ParseIP(parsedHost) == nil {
			return "", errors.New("invalid request host")
		}
		host, port = parsedHost, parsedPort
	} else if strings.Count(raw, ":") == 1 {
		parsedHost, parsedPort, err := net.SplitHostPort(raw)
		if err != nil {
			return "", errors.New("invalid request host")
		}
		host, port = parsedHost, parsedPort
	} else if strings.Contains(raw, ":") || net.ParseIP(raw) != nil {
		return "", errors.New("invalid request host")
	}
	if !validHostName(host) || port != "" && !validPort(port) {
		return "", errors.New("invalid request host")
	}
	host = strings.ToLower(host)
	if port != "" {
		return net.JoinHostPort(host, port), nil
	}
	return host, nil
}

func validHostName(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.To4() != nil
	}
	if len(host) == 0 || len(host) > 253 || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '-') {
				return false
			}
		}
	}
	return true
}

func validPort(port string) bool {
	if port == "" || len(port) > 5 {
		return false
	}
	value, err := strconv.Atoi(port)
	return err == nil && value > 0 && value <= 65535
}

func userETagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || len(candidate) == len(etag) && subtle.ConstantTimeCompare([]byte(candidate), []byte(etag)) == 1 {
			return true
		}
	}
	return false
}
