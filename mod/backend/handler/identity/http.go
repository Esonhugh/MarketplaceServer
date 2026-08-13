package identity

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Esonhugh/MarketplaceServer/pkg/api"
	"github.com/juanjiTech/jin"
	"github.com/juanjiTech/jin/render"
	"github.com/oklog/ulid/v2"
)

const maximumJSONBodyBytes int64 = 64 << 10

func decodeJSONBody(c *jin.Context, destination any) error {
	if c.Request.Body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maximumJSONBodyBytes))
	var body json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return errors.New("request body must be a JSON object")
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body contains a trailing JSON value")
		}
		return err
	}
	return nil
}

func renderSuccess(c *jin.Context, status int, data any) {
	c.Render(status, render.JSON{Data: api.Success(data)})
}

func renderAPIError(c *jin.Context, status int, code, message string) {
	requestID := ulid.Make().String()
	c.Writer.Header().Set("X-Request-Id", requestID)
	c.Render(status, render.JSON{Data: api.NewError(code, message, requestID)})
}
