package remote

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/domain"
	"github.com/b0balYson/dropsite/internal/mcp"
	"github.com/b0balYson/dropsite/internal/service"
)

// Path is where the endpoint is mounted on the API host.
const Path = "/mcp"

// maxBodyBytes allows 1 MB of file content in base64 plus JSON framing.
const maxBodyBytes = 2 << 20

const actorKey = "actor"

// Options wires the handler into the public listener.
type Options struct {
	// ClientIP returns the client address resolved by the API middleware.
	ClientIP func(*http.Request) string
	// OnAuth is told the authenticated user ID (for access logs).
	OnAuth func(ctx context.Context, userID int64)
	Logger *slog.Logger
}

// rateHit carries a rate-limit verdict from the verifier to the response.
type rateHit struct{ retry string }

type rateKey struct{}

// New returns the /mcp handler: bearer authentication (API token or OAuth
// access token for this resource) in front of a stateless Streamable HTTP
// MCP server.
func New(svc *service.Service, o Options) http.Handler {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.ClientIP == nil {
		o.ClientIP = func(r *http.Request) string {
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				return host
			}
			return r.RemoteAddr
		}
	}
	server := mcp.NewServer(brand.Version, resolver(svc, o.Logger), mcp.Options{Files: true, Forms: true, Cabinet: true, Instructions: mcp.Instructions})
	stream := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: maxBodyBytes})
	verify := func(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
		actor, exp, err := svc.AuthenticateMCP(ctx, token, o.ClientIP(r))
		if err != nil {
			if de := domain.AsError(err); de.Kind == domain.KindRateLimited {
				if hit, ok := ctx.Value(rateKey{}).(*rateHit); ok {
					hit.retry = strconv.FormatInt(max(int64(math.Ceil(de.RetryAfter.Seconds())), 1), 10)
				}
			} else if de.Kind == domain.KindInternal {
				return nil, fmt.Errorf("authentication failed: %w", errors.New("internal error"))
			}
			return nil, auth.ErrInvalidToken
		}
		if o.OnAuth != nil {
			o.OnAuth(ctx, actor.User.ID)
		}
		return &auth.TokenInfo{
			Scopes: []string{service.OAuthScope}, Expiration: exp, UserID: strconv.FormatInt(actor.User.ID, 10),
			Extra: map[string]any{actorKey: actor},
		}, nil
	}
	protect := auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    svc.APIURL() + "/.well-known/oauth-protected-resource" + Path,
		Scopes:                 []string{service.OAuthScope},
		AllowMissingExpiration: true,
	})
	inner := protect(stream)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := &rateHit{}
		r = r.WithContext(context.WithValue(r.Context(), rateKey{}, hit))
		inner.ServeHTTP(&rateWriter{ResponseWriter: w, hit: hit}, r)
	})
}

// rateWriter turns the 401 written for a rate-limited verification into 429.
type rateWriter struct {
	http.ResponseWriter
	hit *rateHit
}

func (w *rateWriter) WriteHeader(code int) {
	if code == http.StatusUnauthorized && w.hit.retry != "" {
		w.Header().Del("WWW-Authenticate")
		w.Header().Set("Retry-After", w.hit.retry)
		code = http.StatusTooManyRequests
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *rateWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *rateWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var errNoActor = errors.New("this request is not authenticated; reconnect dropsite")

func resolver(svc *service.Service, log *slog.Logger) mcp.Resolver {
	return func(_ context.Context, req *sdk.CallToolRequest) (mcp.Backend, error) {
		if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
			return nil, errNoActor
		}
		actor, ok := req.Extra.TokenInfo.Extra[actorKey].(service.Actor)
		if !ok {
			return nil, errNoActor
		}
		return backend{svc: svc, actor: actor, log: log}, nil
	}
}
