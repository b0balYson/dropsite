package client

import (
	"context"
	"encoding/base64"
	"net/http"

	"github.com/b0balYson/dropsite/internal/designref"
)

// ReferenceShot is the screenshot part of a reference brief.
type ReferenceShot struct {
	Status   string `json:"status"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	ImagePNG string `json:"image_png"`
}

// Reference is the answer of POST /references/inspect.
type Reference struct {
	designref.Brief
	Screenshot ReferenceShot `json:"screenshot"`
}

// PNG decodes the screenshot; nil when there is none.
func (r Reference) PNG() ([]byte, error) {
	if r.Screenshot.ImagePNG == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(r.Screenshot.ImagePNG)
}

// InspectReference asks the server for the design brief of a reference page.
func (c *Client) InspectReference(ctx context.Context, rawURL string) (*Reference, error) {
	var out Reference
	if err := c.doJSON(ctx, http.MethodPost, "/references/inspect", nil, map[string]string{"url": rawURL}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
