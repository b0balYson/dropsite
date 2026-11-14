package oauth

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/service"
)

const maxBodyBytes = 64 << 10

// LinkResourcePattern is the connect URL path (/mcp/c/<code>) as a route
// pattern; its protected resource metadata lives under ResourceMeta (RFC 9728
// path insertion).
const LinkResourcePattern = "/mcp/c/{code}"

func (s *Server) resourceMetadata() http.Handler {
	return auth.ProtectedResourceMetadataHandler(s.metadataFor(s.svc.MCPResource()))
}

func (s *Server) metadataFor(resource string) *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource:               resource,
		AuthorizationServers:   []string{s.Issuer()},
		ScopesSupported:        []string{service.OAuthScope},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           brand.DisplayName,
	}
}

// linkResourceMetadata is the metadata of a connect URL: the resource is the
// connect URL itself, so a client sends it back as the resource indicator of
// its authorization request (RFC 8707) and the consent page finds the code.
// The answer is the same for every well-formed code, used or not, so it
// tells nothing about codes; anything else is 404.
func (s *Server) linkResourceMetadata(w http.ResponseWriter, r *http.Request) {
	seg := r.PathValue("code")
	if domain.ConnectCodeFromLinkPath(seg) == "" {
		http.NotFound(w, r)
		return
	}
	// The address exactly as the client used it: clients check that the
	// metadata names the URL they were given.
	w.Header().Set("Cache-Control", "no-store")
	auth.ProtectedResourceMetadataHandler(s.metadataFor(s.svc.MCPResource()+domain.ConnectLinkSegment+seg)).ServeHTTP(w, r)
}

// serverMetadata is RFC 8414 metadata.
func (s *Server) serverMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET")
		return
	}
	iss := s.Issuer()
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + AuthorizePath,
		"token_endpoint":                                 iss + TokenPath,
		"registration_endpoint":                          iss + RegisterPath,
		"revocation_endpoint":                            iss + RevokePath,
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"scopes_supported":                               []string{service.OAuthScope},
		"client_id_metadata_document_supported":          s.o.ClientMetadataDocuments,
		"authorization_response_iss_parameter_supported": true,
	})
}

type registerRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

type registerResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

func subset(have, allowed []string) bool {
	for _, v := range have {
		if !slices.Contains(allowed, v) {
			return false
		}
	}
	return true
}

// register is dynamic client registration (RFC 7591), public clients only.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_client_metadata", Description: "the body must be a JSON object"})
		return
	}
	if req.TokenEndpointAuthMethod != "" && req.TokenEndpointAuthMethod != "none" {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_client_metadata",
			Description: "only public clients are supported: token_endpoint_auth_method must be none"})
		return
	}
	grants := []string{"authorization_code", "refresh_token"}
	if !subset(req.GrantTypes, grants) || !subset(req.ResponseTypes, []string{"code"}) {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_client_metadata",
			Description: "supported grant_types: authorization_code, refresh_token; response_types: code"})
		return
	}
	c, err := s.svc.RegisterOAuthClient(r.Context(), req.ClientName, req.RedirectURIs, s.o.ClientIP(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, registerResponse{
		ClientID: c.ID, ClientIDIssuedAt: c.CreatedAt.Unix(), ClientName: c.Name, RedirectURIs: c.RedirectURIs,
		TokenEndpointAuthMethod: "none", GrantTypes: grants, ResponseTypes: []string{"code"},
	})
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// parseForm reads a bounded url-encoded body and rejects repeated parameters.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_request", Description: "the body must be application/x-www-form-urlencoded"})
		return false
	}
	for k, v := range r.PostForm {
		if len(v) > 1 {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_request", Description: "the parameter " + k + " is repeated"})
			return false
		}
	}
	return true
}

// clientID takes client_id from the body or from HTTP Basic credentials.
func clientID(r *http.Request) string {
	if id := r.PostForm.Get("client_id"); id != "" {
		return id
	}
	if id, _, ok := r.BasicAuth(); ok {
		return id
	}
	return ""
}

// token is the token endpoint (authorization_code and refresh_token).
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if !parseForm(w, r) {
		return
	}
	f := r.PostForm
	cid := clientID(r)
	if cid == "" {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "invalid_client", Description: "client_id is required"})
		return
	}
	var (
		out service.OAuthTokens
		err error
	)
	switch f.Get("grant_type") {
	case "authorization_code":
		if f.Get("code") == "" || f.Get("code_verifier") == "" {
			writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid_request", Description: "code and code_verifier are required"})
			return
		}
		out, err = s.svc.ExchangeOAuthCode(r.Context(), f.Get("code"), f.Get("code_verifier"), cid,
			f.Get("redirect_uri"), f.Get("resource"))
	case "refresh_token":
		out, err = s.svc.RefreshOAuth(r.Context(), f.Get("refresh_token"), cid, f.Get("resource"))
	default:
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "unsupported_grant_type",
			Description: "supported: authorization_code, refresh_token"})
		return
	}
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: out.AccessToken, TokenType: "Bearer", ExpiresIn: out.ExpiresIn,
		RefreshToken: out.RefreshToken, Scope: out.Scope,
	})
}

// revoke is RFC 7009 revocation: it always answers 200.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if !parseForm(w, r) {
		return
	}
	if err := s.svc.RevokeOAuthToken(r.Context(), r.PostForm.Get("token")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
