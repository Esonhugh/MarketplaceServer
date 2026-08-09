package jin

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	jinengine "github.com/juanjiTech/jin"
	"github.com/soheilhy/cmux"
)

var _ kernel.Module = (*Mod)(nil)

type Config struct {
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout" mapstructure:"readHeaderTimeout"`
	ReadTimeout       time.Duration `yaml:"readTimeout" mapstructure:"readTimeout"`
	WriteTimeout      time.Duration `yaml:"writeTimeout" mapstructure:"writeTimeout"`
	IdleTimeout       time.Duration `yaml:"idleTimeout" mapstructure:"idleTimeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdownTimeout" mapstructure:"shutdownTimeout"`
}

type Mod struct {
	kernel.UnimplementedModule

	config Config
	engine *jinengine.Engine
	server *http.Server
}

func (m *Mod) Name() string { return "jin" }

func (m *Mod) Config() any {
	m.applyDefaults()
	return &m.config
}

func (m *Mod) PreInit(hub *kernel.Hub) error {
	m.applyDefaults()
	m.engine = jinengine.New()
	m.engine.Use(jinengine.Recovery())
	hub.Map(&m.engine)
	return nil
}

func (m *Mod) Start(hub *kernel.Hub) error {
	var tcpMux cmux.CMux
	if err := hub.Load(&tcpMux); err != nil {
		return errors.New("can't load cmux.CMux from kernel")
	}

	m.server = &http.Server{
		Handler:           m.engine,
		ReadHeaderTimeout: m.config.ReadHeaderTimeout,
		ReadTimeout:       m.config.ReadTimeout,
		WriteTimeout:      m.config.WriteTimeout,
		IdleTimeout:       m.config.IdleTimeout,
	}

	listener := tcpMux.Match(cmux.HTTP1Fast())
	if err := m.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (m *Mod) Stop(wg *sync.WaitGroup, ctx context.Context) error {
	defer wg.Done()
	if m.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, m.config.ShutdownTimeout)
	defer cancel()
	return m.server.Shutdown(ctx)
}

func (m *Mod) applyDefaults() {
	if m.config.ReadHeaderTimeout == 0 {
		m.config.ReadHeaderTimeout = 5 * time.Second
	}
	if m.config.ReadTimeout == 0 {
		m.config.ReadTimeout = 30 * time.Second
	}
	if m.config.WriteTimeout == 0 {
		m.config.WriteTimeout = 30 * time.Second
	}
	if m.config.IdleTimeout == 0 {
		m.config.IdleTimeout = 60 * time.Second
	}
	if m.config.ShutdownTimeout == 0 {
		m.config.ShutdownTimeout = 10 * time.Second
	}
}
