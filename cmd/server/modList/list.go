package modList

import (
	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/Esonhugh/MarketplaceServer/mod/b2x"
	"github.com/Esonhugh/MarketplaceServer/mod/grpcGateway"
	"github.com/Esonhugh/MarketplaceServer/mod/jinPprof"
	"github.com/Esonhugh/MarketplaceServer/mod/jinx"
	"github.com/Esonhugh/MarketplaceServer/mod/myDB"
	"github.com/Esonhugh/MarketplaceServer/mod/pyroscope"
	"github.com/Esonhugh/MarketplaceServer/mod/rds"
	"github.com/Esonhugh/MarketplaceServer/mod/uptrace"
)

var ModList = []kernel.Module{
	&b2x.Mod{},
	&grpcGateway.Mod{},
	&jinPprof.Mod{},
	&jinx.Mod{},
	&myDB.Mod{},
	&pyroscope.Mod{},
	&rds.Mod{},
	&uptrace.Mod{},
}
