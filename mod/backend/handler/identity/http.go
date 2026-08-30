package identity

import (
	managementhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/management"
	"github.com/juanjiTech/jin"
)

const maximumJSONBodyBytes = managementhandler.MaximumJSONBodyBytes

func decodeJSONBody(c *jin.Context, destination any) error {
	return managementhandler.DecodeJSONBody(c, destination)
}

func renderSuccess(c *jin.Context, status int, data any) {
	managementhandler.RenderSuccess(c, status, data)
}

func renderAPIError(c *jin.Context, status int, code, message string) {
	managementhandler.RenderError(c, status, code, message)
}
