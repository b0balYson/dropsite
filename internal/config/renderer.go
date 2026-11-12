package config

// Renderer configures the optional screenshot renderer (docs/screenshots.md).
// control never runs a browser itself: it calls the renderer over the
// internal network and serves it the release files on RenderAddr.
type Renderer struct {
	// URL is the renderer's base URL (e.g. http://renderer:3000); empty
	// disables screenshots and visual diffs.
	URL string
	// Addr is the listener the renderer fetches release files from; it is
	// started only when URL is set.
	Addr string
	// BaseURL is Addr as the renderer reaches it.
	BaseURL string
}

// Enabled reports whether screenshots are configured.
func (r Renderer) Enabled() bool { return r.URL != "" }

func (r *reader) renderer() Renderer {
	out := Renderer{Addr: r.str("RENDER_ADDR", ":8083")}
	if raw := r.str("RENDERER_URL", ""); raw != "" {
		out.URL = r.httpURL("RENDERER_URL", "")
		out.BaseURL = r.httpURL("RENDER_BASE_URL", "http://control:8083")
	}
	return out
}
