package oauth

// knownClients are copies of the client metadata documents of widely used MCP
// clients. They are used only when the live document cannot be fetched: some
// publishers put their documents behind bot protection that refuses requests
// from data-centre addresses, which is where Dropsite servers live. The live
// document always wins when it loads, so a publisher's change takes effect
// without a Dropsite release.
var knownClients = map[string]Client{
	"https://claude.ai/oauth/claude-code-client-metadata": {
		ID:           "https://claude.ai/oauth/claude-code-client-metadata",
		Name:         "Claude Code",
		RedirectURIs: []string{"http://localhost/callback", "http://127.0.0.1/callback"},
		Metadata:     true,
	},
}

// knownClient returns a copy of the built-in metadata of clientID.
func knownClient(clientID string) (Client, bool) {
	c, ok := knownClients[clientID]
	if !ok {
		return Client{}, false
	}
	c.RedirectURIs = append([]string(nil), c.RedirectURIs...)
	return c, true
}
