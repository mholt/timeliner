package timeliner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/gob"
	"fmt"
	"log"
	"time"
)

func init() {
	tdBuf := new(bytes.Buffer)
	err := gob.NewEncoder(tdBuf).Encode(Metadata{})
	if err != nil {
		log.Fatalf("[FATAL] Unable to gob-encode metadata struct: %v", err)
	}
	metadataGobPrefix = tdBuf.Bytes()
}

// RegisterDataSource registers ds as a data source.
func RegisterDataSource(ds DataSource) error {
	if ds.ID == "" {
		return fmt.Errorf("missing ID")
	}
	if ds.Name == "" {
		return fmt.Errorf("missing Name")
	}
	if ds.OAuth2.ProviderID != "" && ds.Authenticate != nil {
		return fmt.Errorf("conflicting ways of obtaining authorization")
	}

	// register the data source
	if _, ok := dataSources[ds.ID]; ok {
		return fmt.Errorf("data source already registered: %s", ds.ID)
	}
	dataSources[ds.ID] = ds

	return nil
}

func saveAllDataSources(db *sql.DB) error {
	if len(dataSources) == 0 {
		return nil
	}

	query := `INSERT INTO "data_sources" ("id", "name") VALUES`
	var vals []interface{}
	var count int

	for _, ds := range dataSources {
		if count > 0 {
			query += ","
		}
		query += " (?, ?)"
		vals = append(vals, ds.ID, ds.Name)
		count++
	}

	query += " ON CONFLICT DO NOTHING"

	_, err := db.Exec(query, vals...)
	if err != nil {
		return fmt.Errorf("writing data sources to DB: %v", err)
	}

	return nil
}

// DataSource has information about a
// data source that can be registered.
type DataSource struct {
	// A snake_cased name of the service
	// that uniquely identifies it from
	// all others.
	ID string

	// The human-readable or brand name of
	// the service.
	Name string

	// If the service authenticates with
	// OAuth2, fill out this field.
	OAuth2 OAuth2

	// Otherwise, if the service uses some
	// other form of authentication,
	// Authenticate is a function which
	// returns the credentials needed to
	// access an account on the service.
	Authenticate AuthenticateFn

	// If the service enforces a rate limit,
	// specify it here. You can abide it by
	// getting an http.Client from the
	// Account passed into NewClient.
	RateLimit RateLimit

	// NewClient is a function which takes
	// information about the account and
	// returns a type which can facilitate
	// transactions with the service.
	NewClient NewClientFn
}

// authFunc gets the authentication function for this
// service. If s.Authenticate is set, it returns that;
// if s.OAuth2 is set, it uses a standard OAuth2 func.
// TODO: update godoc
func (ds DataSource) authFunc() AuthenticateFn {
	if ds.Authenticate != nil {
		return ds.Authenticate
	} else if ds.OAuth2.ProviderID != "" {
		return func(userID string) ([]byte, error) {
			return authorizeWithOAuth2(ds.OAuth2)
		}
	}
	return nil
}

// OAuth2 defines which OAuth2 provider a service
// uses and which scopes it requires.
type OAuth2 struct {
	// The ID of the service must be recognized
	// by the OAuth2 app configuration.
	ProviderID string

	// The list of scopes to ask for during auth.
	Scopes []string
}

// AuthenticateFn is a function that authenticates userID with a service.
// It returns the authorization or credentials needed to operate. The return
// value should be byte-encoded so it can be stored in the DB to be reused.
// To store arbitrary types, encode the value as a gob, for example.
type AuthenticateFn func(userID string) ([]byte, error)

// NewClientFn is a function that returns a client which, given
// the account passed in, can interact with a service provider.
type NewClientFn func(acc Account) (Client, error)

// Client is a type that can interact with a data source.
type Client interface {
	// ListItems lists the items on the account. Items should be
	// sent on itemChan as they are discovered, but related items
	// should be combined onto a single ItemGraph so that their
	// relationships can be stored. If the relationships are not
	// discovered until later, that's OK: item processing is
	// idempotent, so repeating an item from earlier will have no
	// adverse effects (this is possible because a unique ID is
	// required for each item).
	//
	// Implementations must honor the context's cancellation. If
	// ctx.Done() is closed, the function should return. Typically,
	// this is done by having an outer loop select over ctx.Done()
	// and default, where the next page or set of items is handled
	// in the default case.
	//
	// ListItems MUST close itemChan when returning. A
	// `defer close(itemChan)` will usually suffice. Closing
	// this channel signals to the processing goroutine that
	// no more items are coming.
	//
	// Further options for listing items may be passed in opt.
	//
	// If opt.Filename is specified, the implementation is expected
	// to open and list items from that file. If this is not
	// supported, an error should be returned. Conversely, if a
	// filename is not specified but required, an error should be
	// returned.
	//
	// opt.Timeframe consists of two optional timestamp and/or item
	// ID values. If set, item listings should be bounded in the
	// respective direction by that timestamp / item ID. (Items
	// are assumed to be part of a chronology; both timestamp and
	// item ID *may be* provided, when possible, to accommodate
	// data sources which do not constrain by timestamp but which
	// do by item ID instead.) The respective time and item ID
	// fields, if set, will not be in conflict, so either may be
	// used if both are present. While it should be documented if
	// timeframes are not supported, an error need not be returned
	// if they cannot be honored.
	//
	// opt.Checkpoint consists of the last checkpoint for this
	// account if the last call to ListItems did not finish and
	// if a checkpoint was saved. If not nil, the checkpoint
	// should be used to resume the listing instead of starting
	// over from the beginning. Checkpoint values usually consist
	// of page tokens or whatever state is required to resume. Call
	// timeliner.Checkpoint to set a checkpoint. Checkpoints are not
	// required, but if the implementation sets checkpoints, it
	// should be able to resume from one, too.
	ListItems(ctx context.Context, itemChan chan<- *ItemGraph, opt Options) error
}

// Timeframe represents a start and end time and/or
// a start and end item, where either value could be
// nil which means unbounded in that direction.
// When items are used as the timeframe boundaries,
// the ItemID fields will be populated. It is not
// guaranteed that any particular field will be set
// or unset just because other fields are set or unset.
// However, if both Since or both Until fields are
// set, that means the timestamp and items are
// correlated; i.e. the Since timestamp is (approx.)
// that of the item ID. Or, put another way: there
// will never be conflicts among the fields which
// are non-nil.
type Timeframe struct {
	Since, Until             *time.Time
	SinceItemID, UntilItemID *string
}

func (tf Timeframe) String() string {
	var sinceItemID, untilItemID string
	if tf.SinceItemID != nil {
		sinceItemID = *tf.SinceItemID
	}
	if tf.UntilItemID != nil {
		untilItemID = *tf.UntilItemID
	}
	return fmt.Sprintf("{Since:%s Until:%s SinceItemID:%s UntilItemID:%s}",
		tf.Since, tf.Until, sinceItemID, untilItemID)
}

var dataSources = make(map[string]DataSource)
