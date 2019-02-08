package oauth2client

import (
	"context"
	mathrand "math/rand"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

func init() {
	mathrand.Seed(time.Now().UnixNano())
}

// Getter is a type that can get an OAuth2 auth code.
// It must enforce that the state parameter of the
// redirected request matches expectedStateVal.
type Getter interface {
	Get(expectedStateVal, authCodeURL string) (code string, err error)
}

// State returns a random string suitable as a state value.
func State() string {
	return randString(14)
}

// randString is not safe for cryptographic use.
func randString(n int) string {
	const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[mathrand.Intn(len(letterBytes))]
	}
	return string(b)
}

type (
	// OAuth2Info contains information for obtaining an auth code.
	OAuth2Info struct {
		StateValue  string
		AuthCodeURL string
	}

	// App provides a way to get an initial OAuth2 token
	// as well as a continuing token source.
	App interface {
		InitialToken() (*oauth2.Token, error)
		TokenSource(context.Context, *oauth2.Token) oauth2.TokenSource
	}
)

// httpClient is the HTTP client to use for OAuth2 requests.
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// DefaultRedirectURL is the default URL to
// which to redirect clients after a code
// has been obtained. Redirect URLs may
// have to be registered with your OAuth2
// provider.
const DefaultRedirectURL = "http://localhost:8008/oauth2-redirect"
