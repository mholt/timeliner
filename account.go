package timeliner

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Account represents an account with a service.
type Account struct {
	ID            int64
	DataSourceID  string
	UserID        string
	person        Person
	authorization []byte
	checkpoint    []byte
	lastItemID    *int64

	t  *Timeline
	ds DataSource
}

// NewHTTPClient returns an HTTP client that is suitable for use
// with an API associated with the account's data source. If
// OAuth2 is configured for the data source, the client has OAuth2
// credentials. If a rate limit is configured, this client is
// rate limited. A sane default timeout is set, and any fields
// on the returned Client valule can be modified as needed.
func (acc Account) NewHTTPClient() (*http.Client, error) {
	httpClient := new(http.Client)
	if acc.ds.OAuth2.ProviderID != "" {
		var err error
		httpClient, err = acc.NewOAuth2HTTPClient()
		if err != nil {
			return nil, err
		}
	}
	if acc.ds.RateLimit.RequestsPerHour > 0 {
		httpClient.Transport = acc.NewRateLimitedRoundTripper(httpClient.Transport)
	}
	httpClient.Timeout = 60 * time.Second
	return httpClient, nil
}

// AddAccount authenticates userID with the service identified
// within the application by dataSourceID, and then stores it in the
// database.
func (t *Timeline) AddAccount(dataSourceID, userID string) error {
	ds, ok := dataSources[dataSourceID]
	if !ok {
		return fmt.Errorf("data source not registered: %s", dataSourceID)
	}

	// ensure account is not already stored in our system
	var count int
	err := t.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE data_source_id=? AND user_id=? LIMIT 1`,
		dataSourceID, userID).Scan(&count)
	if err != nil {
		return fmt.Errorf("checking if account is already stored: %v", err)
	}
	if count > 0 {
		return fmt.Errorf("account already stored in database: %s/%s", dataSourceID, userID)
	}

	// authenticate with the data source (if necessary)
	var credsBytes []byte
	if authFn := ds.authFunc(); authFn != nil {
		credsBytes, err = authFn(userID)
		if err != nil {
			return fmt.Errorf("authenticating %s for %s: %v", userID, dataSourceID, err)
		}
	}

	// make sure the data source is registered in the DB
	_, err = t.db.Exec(`INSERT OR IGNORE INTO data_sources (id, name) VALUES (?, ?)`,
		dataSourceID, ds.Name)
	if err != nil {
		return fmt.Errorf("saving data source record: %v", err)
	}

	// store the account along with our authorization to access it
	_, err = t.db.Exec(`INSERT INTO accounts (data_source_id, user_id, authorization) VALUES (?, ?, ?)`,
		dataSourceID, userID, credsBytes)
	if err != nil {
		return fmt.Errorf("inserting into DB: %v", err)
	}

	return nil
}

// NewClient returns a new Client that is ready to interact with
// the data source for the account uniquely specified by the data
// source ID and the user ID for that data source. The Client is
// actually wrapped by a type with unexported fields that are
// necessary for internal use.
func (t *Timeline) NewClient(dataSourceID, userID string) (WrappedClient, error) {
	ds, ok := dataSources[dataSourceID]
	if !ok {
		return WrappedClient{}, fmt.Errorf("data source not registered: %s", dataSourceID)
	}
	if ds.NewClient == nil {
		return WrappedClient{}, fmt.Errorf("impossible to make client for data source: %s", dataSourceID)
	}

	acc, err := t.getAccount(dataSourceID, userID)
	if err != nil {
		return WrappedClient{}, fmt.Errorf("getting account: %v", err)
	}

	cl, err := ds.NewClient(acc)
	if err != nil {
		return WrappedClient{}, fmt.Errorf("making client from data source: %v", err)
	}

	return WrappedClient{
		Client:     cl,
		tl:         t,
		acc:        acc,
		ds:         ds,
		lastItemMu: new(sync.Mutex),
	}, nil
}

func (t *Timeline) getAccount(dsID, userID string) (Account, error) {
	ds, ok := dataSources[dsID]
	if !ok {
		return Account{}, fmt.Errorf("data source not registered: %s", dsID)
	}
	acc := Account{
		ds: ds,
		t:  t,
	}
	err := t.db.QueryRow(`SELECT
		id, data_source_id, user_id, authorization, checkpoint, last_item_id
		FROM accounts WHERE data_source_id=? AND user_id=? LIMIT 1`,
		dsID, userID).Scan(&acc.ID, &acc.DataSourceID, &acc.UserID, &acc.authorization, &acc.checkpoint, &acc.lastItemID)
	if err != nil {
		return acc, fmt.Errorf("querying account %s/%s from DB: %v", dsID, userID, err)
	}
	return acc, nil
}

// MarshalGob is a convenient way to gob-encode v.
func MarshalGob(v interface{}) ([]byte, error) {
	b := new(bytes.Buffer)
	err := gob.NewEncoder(b).Encode(v)
	return b.Bytes(), err
}

// UnmarshalGob is a convenient way to gob-decode data into v.
func UnmarshalGob(data []byte, v interface{}) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}
