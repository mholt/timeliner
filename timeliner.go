// Timeliner - A personal data aggregation utility
// Copyright (C) 2019 Matthew Holt
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// TODO: Apply license to all files

package timeliner

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	"sync"
	"time"

	cuckoo "github.com/seiflotfy/cuckoofilter"
)

func init() {
	mathrand.Seed(time.Now().UnixNano())
}

// Timeline represents an opened timeline repository.
// The zero value is NOT valid; use Open() to obtain
// a valid value.
type Timeline struct {
	db           *sql.DB
	repoDir      string
	rateLimiters map[string]RateLimit
}

// Open creates/opens a timeline at the given
// repository directory. Timelines should always
// be Close()'d for a clean shutdown when done.
func Open(repo string) (*Timeline, error) {
	db, err := openDB(repo)
	if err != nil {
		return nil, fmt.Errorf("opening database: %v", err)
	}
	return &Timeline{
		db:           db,
		repoDir:      repo,
		rateLimiters: make(map[string]RateLimit),
	}, nil
}

// Close frees up resources allocated from Open.
func (t *Timeline) Close() error {
	for key, rl := range t.rateLimiters {
		if rl.ticker != nil {
			rl.ticker.Stop()
			rl.ticker = nil
		}
		delete(t.rateLimiters, key)
	}
	if t.db != nil {
		return t.db.Close()
	}
	return nil
}

type concurrentCuckoo struct {
	*cuckoo.Filter
	*sync.Mutex
}

// FakeCloser turns an io.Reader into an io.ReadCloser
// where the Close() method does nothing.
func FakeCloser(r io.Reader) io.ReadCloser {
	return fakeCloser{r}
}

type fakeCloser struct {
	io.Reader
}

// Close does nothing except satisfy io.Closer.
func (fc fakeCloser) Close() error { return nil }

// ctxKey is used for contexts, as recommended by
// https://golang.org/pkg/context/#WithValue. It
// is unexported so values stored by this package
// can only be accessed by this package.
type ctxKey string

// wrappedClientCtxKey is how the context value is accessed.
var wrappedClientCtxKey ctxKey = "wrapped_client"

// CheckpointFn is a function that saves a checkpoint.
type CheckpointFn func(checkpoint []byte) error

// Checkpoint saves a checkpoint for the processing associated
// with the provided context. It overwrites any previous
// checkpoint. Any errors are logged.
func Checkpoint(ctx context.Context, checkpoint []byte) {
	wc, ok := ctx.Value(wrappedClientCtxKey).(*WrappedClient)

	if !ok {
		log.Printf("[ERROR][%s/%s] Checkpoint function not available; got type %T (%#v)",
			wc.ds.ID, wc.acc.UserID, wc, wc)
		return
	}

	chkpt, err := MarshalGob(checkpointWrapper{wc.commandParams, checkpoint})
	if err != nil {
		log.Printf("[ERROR][%s/%s] Encoding checkpoint wrapper: %v", wc.ds.ID, wc.acc.UserID, err)
		return
	}

	_, err = wc.tl.db.Exec(`UPDATE accounts SET checkpoint=? WHERE id=?`, // TODO: LIMIT 1 (see https://github.com/mattn/go-sqlite3/pull/564)
		chkpt, wc.acc.ID)
	if err != nil {
		log.Printf("[ERROR][%s/%s] Checkpoint: %v", wc.ds.ID, wc.acc.UserID, err)
		return
	}
}

// checkpointWrapper stores a provider's checkpoint along with the
// parameters of the command that initiated the process; the checkpoint
// will only be loaded and restored to the provider on next run if
// the parameters match, because it doesn't make sense to restore a
// process that has different, potentially conflicting, parameters,
// such as timeframe.
type checkpointWrapper struct {
	Params string
	Data   []byte
}

// ProcessingOptions configures how item processing is carried out.
type ProcessingOptions struct {
	Reprocess bool
	Prune     bool
	Integrity bool
	Timeframe Timeframe
	Merge     MergeOptions
	Verbose   bool
}

// MergeOptions configures how items are merged. By
// default, items are not merged; if an item with a
// duplicate ID is encountered, it will be replaced
// with the new item (see the "reprocess" flag).
// Merging has to be explicitly enabled.
//
// Currently, the only way to perform a merge is to
// enable "soft" merging: finding an item with the
// same timestamp and either text data or filename.
// Then, one of the item's IDs is updated to match
// the other. These merge options configure how
// the items are then combined.
//
// As it is possible and likely for both items to
// have non-empty values for the same fields, these
// "conflicts" must be resolved non-interactively.
// By default, a merge conflict prefers existing
// values (old item's field) over the new one, and
// the new one only fills in missing values. (This
// seems safest.) However, these merge options allow
// you to customize that behavior and overwrite
// existing values with the new item's fields (only
// happens if new item's field is non-empty, i.e.
// a merge will never delete existing data).
type MergeOptions struct {
	// Enables "soft" merging.
	//
	// If true, an item may be merged if it is likely
	// to be the same as an existing item, even if the
	// item IDs are different. For example, if a
	// service has multiple ways of listing items, but
	// does not provide a consistent ID for the same
	// item across listings, a soft merge will allow the
	// processing to treat them as the same as long as
	// other fields match: timestamp, and either data text
	// or data filename.
	SoftMerge bool

	// Overwrite existing (old) item's ID with the ID
	// provided by the current (new) item.
	PreferNewID bool

	// Overwrite existing item's text data.
	PreferNewDataText bool

	// Overwrite existing item's data file.
	PreferNewDataFile bool

	// Overwrite existing item's metadata.
	PreferNewMetadata bool
}

// ListingOptions specifies parameters for listing items
// from a data source. Some data sources might not be
// able to honor all fields.
type ListingOptions struct {
	// A file from which to read the data.
	Filename string

	// Time bounds on which data to retrieve.
	// The respective time and item ID fields
	// which are set must never conflict.
	Timeframe Timeframe

	// A checkpoint from which to resume
	// item retrieval.
	Checkpoint []byte

	// Enable verbose output (logs).
	Verbose bool
}
