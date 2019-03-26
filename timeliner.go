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

// Options specifies parameters for listing items
// from a data source. Some data sources might not
// be able to honor all fields.
type Options struct {
	// A file from which to read the data.
	Filename string

	// Time bounds on which data to retrieve.
	// The respective time and item ID fields
	// which are set must never conflict.
	Timeframe Timeframe

	// A checkpoint from which to resume
	// item retrieval.
	Checkpoint []byte

	//TODO: Integrate a new timezone parameter for telegram
	// A default timezone to use for timestamps
	// without explicit timezones, e.g. "Europe/Berlin"
	// See https://golang.org/pkg/time/#LoadLocation
	//Timezone string
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

	_, err := wc.tl.db.Exec(`UPDATE accounts SET checkpoint=? WHERE id=?`, // TODO: LIMIT 1 (see https://github.com/mattn/go-sqlite3/pull/564)
		checkpoint, wc.acc.ID)
	if err != nil {
		log.Printf("[ERROR][%s/%s] Checkpoint: %v", wc.ds.ID, wc.acc.UserID, err)
		return
	}
}
