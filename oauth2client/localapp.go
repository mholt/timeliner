package oauth2client

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
)

// LocalAppSource implements oauth2.TokenSource for
// OAuth2 client apps that have the client app
// credentials (Client ID and Secret) available
// locally. The OAuth2 provider is accessed directly
// using the OAuth2Config field value.
//
// LocalAppSource values can be ephemeral.
type LocalAppSource struct {
	// OAuth2Config is the OAuth2 configuration.
	OAuth2Config *oauth2.Config

	// AuthCodeGetter is how the auth code
	// is obtained. If not set, a default
	// oauth2code.Browser is used.
	AuthCodeGetter Getter
}

// Config returns an OAuth2 config.
func (s LocalAppSource) Config() *oauth2.Config {
	return s.OAuth2Config
}

// Token obtains a token using s.OAuth2Config.
func (s LocalAppSource) Token() (*oauth2.Token, error) {
	if s.OAuth2Config == nil {
		return nil, fmt.Errorf("missing OAuth2Config")
	}
	if s.AuthCodeGetter == nil {
		s.AuthCodeGetter = Browser{}
	}

	cfg := s.Config()

	stateVal := State()
	authURL := cfg.AuthCodeURL(stateVal, oauth2.AccessTypeOffline)

	code, err := s.AuthCodeGetter.Get(stateVal, authURL)
	if err != nil {
		return nil, fmt.Errorf("getting code via browser: %v", err)
	}

	ctx := context.WithValue(context.Background(),
		oauth2.HTTPClient, httpClient)

	return cfg.Exchange(ctx, code)
}

var _ App = LocalAppSource{}
