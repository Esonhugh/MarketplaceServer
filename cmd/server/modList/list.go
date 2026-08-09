package modList

import (
	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/Esonhugh/MarketplaceServer/mod/backend"
	"github.com/Esonhugh/MarketplaceServer/mod/frontend"
	"github.com/Esonhugh/MarketplaceServer/mod/git"
	"github.com/Esonhugh/MarketplaceServer/mod/jin"
	"github.com/Esonhugh/MarketplaceServer/mod/sql"
)

var ModList = []kernel.Module{
	&jin.Mod{},
	&sql.Mod{},
	&git.Mod{},
	&backend.Mod{},
	&frontend.Mod{},
}
