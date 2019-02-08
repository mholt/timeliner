package timeliner

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mholt/timeliner/oauth2client"
	"golang.org/x/oauth2"
)

// OAuth2AppSource returns an oauth2client.App for the OAuth2 provider
// with the given ID. Programs using data sources that authenticate
// with OAuth2 MUST set this variable, or the program will panic.
var OAuth2AppSource func(providerID string, scopes []string) (oauth2client.App, error)

// NewOAuth2HTTPClient returns a new HTTP client which performs
// HTTP requests that are authenticated with an oauth2.Token
// stored with the account acc.
func (acc Account) NewOAuth2HTTPClient() (*http.Client, error) {
	// load the existing token for this account from the database
	var tkn *oauth2.Token
	err := UnmarshalGob(acc.authorization, &tkn)
	if err != nil {
		return nil, fmt.Errorf("gob-decoding OAuth2 token: %v", err)
	}
	if tkn == nil || tkn.AccessToken == "" {
		return nil, fmt.Errorf("OAuth2 token is empty: %+v", tkn)
	}

	// load the service's "oauth app", which can provide both tokens and
	// oauth configs -- in this case, we need the oauth config; we should
	// already have a token
	oapp, err := OAuth2AppSource(acc.ds.OAuth2.ProviderID, acc.ds.OAuth2.Scopes)
	if err != nil {
		return nil, fmt.Errorf("getting token source for %s: %v", acc.DataSourceID, err)
	}

	// obtain a token source from the oauth's config so that it can keep
	// the token refreshed if it expires
	src := oapp.TokenSource(context.Background(), tkn)

	// finally, create an HTTP client that authenticates using the token,
	// but wrapping the underlying token source so we can persist any
	// changes to the database
	return oauth2.NewClient(context.Background(), &persistedTokenSource{
		tl:        acc.t,
		ts:        src,
		accountID: acc.ID,
		token:     tkn,
	}), nil
}

// authorizeWithOAuth2 gets an initial OAuth2 token from the user.
// It requires OAuth2AppSource to be set or it will panic.
func authorizeWithOAuth2(oc OAuth2) ([]byte, error) {
	src, err := OAuth2AppSource(oc.ProviderID, oc.Scopes)
	if err != nil {
		return nil, fmt.Errorf("getting token source: %v", err)
	}
	tkn, err := src.InitialToken()
	if err != nil {
		return nil, fmt.Errorf("getting token from source: %v", err)
	}
	return MarshalGob(tkn)
}

// persistedTokenSource wraps a TokenSource for
// a particular account and persists any changes
// to the account's token to the database.
type persistedTokenSource struct {
	tl        *Timeline
	ts        oauth2.TokenSource
	accountID int64
	token     *oauth2.Token
}

func (ps *persistedTokenSource) Token() (*oauth2.Token, error) {
	tkn, err := ps.ts.Token()
	if err != nil {
		return tkn, err
	}

	// store an updated token in the DB
	if tkn.AccessToken != ps.token.AccessToken {
		ps.token = tkn

		authBytes, err := MarshalGob(tkn)
		if err != nil {
			return nil, fmt.Errorf("gob-encoding new OAuth2 token: %v", err)
		}

		_, err = ps.tl.db.Exec(`UPDATE accounts SET authorization=? WHERE id=?`, authBytes, ps.accountID)
		if err != nil {
			return nil, fmt.Errorf("storing refreshed OAuth2 token: %v", err)
		}
	}

	return tkn, nil
}
