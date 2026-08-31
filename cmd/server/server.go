package server

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Esonhugh/MarketplaceServer/cmd/server/modList"
	"github.com/Esonhugh/MarketplaceServer/conf"
	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/Esonhugh/MarketplaceServer/core/logx"
	"github.com/Esonhugh/MarketplaceServer/pkg/ip"
	"github.com/Esonhugh/MarketplaceServer/pkg/sentry"
	"github.com/soheilhy/cmux"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configPath string
	StartCmd   = &cobra.Command{
		Use:     "server",
		Short:   "Start server",
		Example: "jframe server -c ./config.yaml",
		RunE: func(_ *cobra.Command, _ []string) error {
			return run()
		},
	}
)

func run() (runErr error) {
	log := logx.NameSpace("cmd.server")
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("server lifecycle failed: %v", recovered)
		}
	}()

	log.Info("loading config...")
	// Enable BindStruct to allow unmarshal env into a nested struct
	// https://github.com/spf13/viper/pull/1429
	viper.SetOptions(viper.ExperimentalBindStruct())
	// This line allows viper to use an env var like ORIGIN_VALUE to override the viper string "Origin.Value"
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	if err := conf.LoadConfig(configPath); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log.Info("loading config complete")

	log.Info("init dep...")
	if conf.Get().SentryDsn != "" {
		sentry.Init()
	}
	log.Info("init dep complete")

	log.Info("init kernel...")
	conn, err := net.Listen("tcp", fmt.Sprintf(":%s", conf.Get().Port))
	if err != nil {
		return fmt.Errorf("listen on port %s: %w", conf.Get().Port, err)
	}
	defer conn.Close()
	tcpMux := cmux.New(conn)
	log.Infow("start listening", "port", conf.Get().Port)
	k := kernel.New(kernel.Config{})
	k.Map(&conn, &tcpMux)
	// ModList is a list of module that you want to start
	// the place to add your module is in modList.go
	k.RegMod(modList.ModList...)
	k.Init()
	log.Info("init kernel complete")

	log.Info("init module...")
	if err := k.StartModule(); err != nil {
		return fmt.Errorf("start modules: %w", err)
	}
	log.Info("init module complete")

	log.Info("starting Server...")
	k.Serve()
	go func() {
		_ = tcpMux.Serve()
	}()

	fmt.Println("Server run at:")
	fmt.Printf("-  Local:   http://localhost:%s\n", conf.Get().Port)
	for _, host := range ip.GetLocalHost() {
		fmt.Printf("-  Network: http://%s:%s\n", host, conf.Get().Port)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	<-quit
	fmt.Println("Shutting down server...")

	if err := k.Stop(); err != nil {
		return fmt.Errorf("stop modules: %w", err)
	}
	return nil
}

func init() {
	StartCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Start server with provided configuration file")
}
