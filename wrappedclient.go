package timeliner

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	cuckoo "github.com/seiflotfy/cuckoofilter"
)

// WrappedClient wraps a Client instance with unexported
// fields that contain necessary state for performing
// data collection operations. Do not craft this type
// manually; use Timeline.NewClient() to obtain one.
type WrappedClient struct {
	Client
	tl  *Timeline
	acc Account
	ds  DataSource

	lastItemRowID     int64
	lastItemTimestamp time.Time
	lastItemMu        *sync.Mutex

	// used with checkpoints; it only makes sense to resume a checkpoint
	// if the process has the same operational parameters as before;
	// some providers (like Google Photos) even return errors if you
	// query a "next page" with different parameters
	commandParams string
}

// GetLatest gets the most recent items from wc. It does not prune or
// reprocess; only meant for a quick pull (error will be returned if
// procOpt is not compatible). If there are no items pulled yet, all
// items will be pulled. If procOpt.Timeframe.Until is not nil, the
// latest only up to that timestamp will be pulled, and if until is
// after the latest item, no items will be pulled.
func (wc *WrappedClient) GetLatest(ctx context.Context, procOpt ProcessingOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, wrappedClientCtxKey, wc)

	if procOpt.Reprocess || procOpt.Prune || procOpt.Integrity || procOpt.Timeframe.Since != nil {
		return fmt.Errorf("get-latest does not support -reprocess, -prune, -integrity, or -start")
	}

	// get date and original ID of the most recent item for this
	// account from the last successful run
	var mostRecentTimestamp int64
	var mostRecentOriginalID string
	if wc.acc.lastItemID != nil {
		err := wc.tl.db.QueryRow(`SELECT timestamp, original_id
		FROM items WHERE id=? LIMIT 1`, *wc.acc.lastItemID).Scan(&mostRecentTimestamp, &mostRecentOriginalID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("getting most recent item: %v", err)
		}
	}

	// constrain the pull to the recent timeframe
	timeframe := Timeframe{Until: procOpt.Timeframe.Until}
	if mostRecentTimestamp > 0 && timeframe.Until != nil {
		ts := time.Unix(mostRecentTimestamp, 0)
		timeframe.Since = &ts
		if timeframe.Until.Before(ts) {
			// most recent item is already after "until"/end date; nothing to do
			return nil
		}
	}
	if mostRecentOriginalID != "" {
		timeframe.SinceItemID = &mostRecentOriginalID
	}

	checkpoint := wc.prepareCheckpoint(timeframe)

	wg, ch := wc.beginProcessing(concurrentCuckoo{}, procOpt)

	err := wc.Client.ListItems(ctx, ch, ListingOptions{
		Timeframe:  timeframe,
		Checkpoint: checkpoint,
		Verbose:    procOpt.Verbose,
	})
	if err != nil {
		return fmt.Errorf("getting items from service: %v", err)
	}

	// wait for processing to complete
	wg.Wait()

	err = wc.successCleanup()
	if err != nil {
		return fmt.Errorf("processing completed, but error cleaning up: %v", err)
	}

	return nil
}

// GetAll gets all the items using wc. If procOpt.Reprocess is true, items that
// are already in the timeline will be re-processed. If procOpt.Prune is true,
// items that are not listed on the data source by wc will be removed
// from the timeline at the end of the listing. If procOpt.Integrity is true,
// all items that are listed by wc that exist in the timeline and which
// consist of a data file will be opened and checked for integrity; if
// the file has changed, it will be reprocessed.
func (wc *WrappedClient) GetAll(ctx context.Context, procOpt ProcessingOptions) error {
	if wc.Client == nil {
		return fmt.Errorf("no client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, wrappedClientCtxKey, wc)

	var cc concurrentCuckoo
	if procOpt.Prune {
		cc.Filter = cuckoo.NewFilter(10000000) // 10mil = ~16 MB on 64-bit
		cc.Mutex = new(sync.Mutex)
	}

	checkpoint := wc.prepareCheckpoint(procOpt.Timeframe)

	wg, ch := wc.beginProcessing(cc, procOpt)

	err := wc.Client.ListItems(ctx, ch, ListingOptions{
		Checkpoint: checkpoint,
		Timeframe:  procOpt.Timeframe,
		Verbose:    procOpt.Verbose,
	})
	if err != nil {
		return fmt.Errorf("getting items from service: %v", err)
	}

	// wait for processing to complete
	wg.Wait()

	err = wc.successCleanup()
	if err != nil {
		return fmt.Errorf("processing completed, but error cleaning up: %v", err)
	}

	// commence prune, if requested
	if procOpt.Prune {
		err := wc.doPrune(cc)
		if err != nil {
			return fmt.Errorf("processing completed, but error pruning: %v", err)
		}
	}

	return nil
}

// prepareCheckpoint sets the current command parameters on wc for
// checkpoints to be saved later on, and then returns the last
// checkpoint data only if its parameters match the new/current ones.
// This prevents trying to resume a process with different parameters
// which can cause errors.
func (wc *WrappedClient) prepareCheckpoint(tf Timeframe) []byte {
	wc.commandParams = tf.String()
	if wc.acc.cp == nil || wc.acc.cp.Params != wc.commandParams {
		return nil
	}
	return wc.acc.cp.Data
}

func (wc *WrappedClient) successCleanup() error {
	// clear checkpoint
	_, err := wc.tl.db.Exec(`UPDATE accounts SET checkpoint=NULL WHERE id=?`, wc.acc.ID) // TODO: limit 1 (see https://github.com/mattn/go-sqlite3/pull/802)
	if err != nil {
		return fmt.Errorf("clearing checkpoint: %v", err)
	}
	wc.acc.checkpoint = nil

	// update the last item ID, to advance the window for future get-latest operations
	wc.lastItemMu.Lock()
	lastItemID := wc.lastItemRowID
	wc.lastItemMu.Unlock()
	if lastItemID > 0 {
		_, err = wc.tl.db.Exec(`UPDATE accounts SET last_item_id=? WHERE id=?`, lastItemID, wc.acc.ID) // TODO: limit 1
		if err != nil {
			return fmt.Errorf("advancing most recent item ID: %v", err)
		}
	}

	return nil
}

// Import is like GetAll but for a locally-stored archive or export file that can
// simply be opened and processed, rather than needing to run over a network. See
// the godoc for GetAll. This is only for data sources that support Import.
func (wc *WrappedClient) Import(ctx context.Context, filename string, procOpt ProcessingOptions) error {
	if wc.Client == nil {
		return fmt.Errorf("no client")
	}

	var cc concurrentCuckoo
	if procOpt.Prune {
		cc.Filter = cuckoo.NewFilter(10000000) // 10mil = ~16 MB on 64-bit
		cc.Mutex = new(sync.Mutex)
	}

	wg, ch := wc.beginProcessing(cc, procOpt)

	err := wc.Client.ListItems(ctx, ch, ListingOptions{
		Filename:   filename,
		Checkpoint: wc.acc.checkpoint,
		Timeframe:  procOpt.Timeframe,
		Verbose:    procOpt.Verbose,
	})
	if err != nil {
		return fmt.Errorf("importing: %v", err)
	}

	// wait for processing to complete
	wg.Wait()

	err = wc.successCleanup()
	if err != nil {
		return fmt.Errorf("processing completed, but error cleaning up: %v", err)
	}

	// commence prune, if requested
	if procOpt.Prune {
		err := wc.doPrune(cc)
		if err != nil {
			return fmt.Errorf("processing completed, but error pruning: %v", err)
		}
	}

	return nil
}

func (wc *WrappedClient) doPrune(cuckoo concurrentCuckoo) error {
	// absolutely do not allow a prune to happen if the account
	// has a checkpoint; this is because we don't store the cuckoo
	// filter with checkpoints, meaning that the list of items
	// that have been seen is INCOMPLETE, and pruning on that
	// would lead to data loss. TODO: Find a way to store the
	// cuckoo filter with a checkpoint...
	var ckpt []byte
	err := wc.tl.db.QueryRow(`SELECT checkpoint FROM accounts WHERE id=? LIMIT 1`,
		wc.acc.ID).Scan(&ckpt)
	if err != nil {
		return fmt.Errorf("querying checkpoint: %v", err)
	}
	if len(ckpt) > 0 {
		return fmt.Errorf("checkpoint exists; refusing to prune for fear of incomplete item listing")
	}

	// deleting items can't happen while iterating the rows
	// since the database table locks; i.e. those two operations
	// are in conflict, so we can't do the delete until we
	// close the result rows; hence, we have to load each
	// item to delete into memory (sigh) and then delete after
	// the listing is complete
	itemsToDelete, err := wc.listItemsToDelete(cuckoo)
	if err != nil {
		return fmt.Errorf("listing items to delete: %v", err)
	}

	for _, rowID := range itemsToDelete {
		err := wc.deleteItem(rowID)
		if err != nil {
			log.Printf("[ERROR][%s/%s] Deleting item: %v (item_id=%d)",
				wc.ds.ID, wc.acc.UserID, err, rowID)
		}
	}

	return nil
}

func (wc *WrappedClient) listItemsToDelete(cuckoo concurrentCuckoo) ([]int64, error) {
	rows, err := wc.tl.db.Query(`SELECT id, original_id FROM items WHERE account_id=?`, wc.acc.ID)
	if err != nil {
		return nil, fmt.Errorf("selecting all items from account: %v (account_id=%d)", err, wc.acc.ID)
	}
	defer rows.Close()

	var itemsToDelete []int64
	for rows.Next() {
		var rowID int64
		var originalID string
		err := rows.Scan(&rowID, &originalID)
		if err != nil {
			return nil, fmt.Errorf("scanning item: %v", err)
		}
		if originalID == "" {
			continue
		}
		cuckoo.Lock()
		existsOnService := cuckoo.Lookup([]byte(originalID))
		cuckoo.Unlock()
		if !existsOnService {
			itemsToDelete = append(itemsToDelete, rowID)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating item rows: %v", err)
	}

	return itemsToDelete, nil
}

func (wc *WrappedClient) deleteItem(rowID int64) error {
	// before deleting the row, find out whether this item
	// has a data file and is the only one referencing it
	var count int
	var dataFile string
	err := wc.tl.db.QueryRow(`SELECT COUNT(*), data_file FROM items
		WHERE data_file = (SELECT data_file FROM items
							WHERE id=? AND data_file IS NOT NULL
							AND data_file != "" LIMIT 1)`,
		rowID).Scan(&count, &dataFile)
	if err != nil {
		return fmt.Errorf("querying count of rows sharing data file: %v", err)
	}

	_, err = wc.tl.db.Exec(`DELETE FROM items WHERE id=?`, rowID) // TODO: limit 1 (see https://github.com/mattn/go-sqlite3/pull/802)
	if err != nil {
		return fmt.Errorf("deleting item from DB: %v", err)
	}

	if count == 1 {
		err := os.Remove(wc.tl.fullpath(dataFile))
		if err != nil {
			return fmt.Errorf("deleting associated data file from disk: %v", err)
		}
	}

	return nil
}

// DataSourceName returns the name of the data source wc was created from.
func (wc *WrappedClient) DataSourceName() string { return wc.ds.Name }

// DataSourceID returns the ID of the data source wc was created from.
func (wc *WrappedClient) DataSourceID() string { return wc.ds.ID }

// UserID returns the ID of the user associated with this client.
func (wc *WrappedClient) UserID() string { return wc.acc.UserID }
