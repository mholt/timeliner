package timeliner

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// beginProcessing starts workers to process items that are
// obtained from ac. It returns a WaitGroup which blocks until
// all workers have finished, and a channel into which the
// service should pipe its items.
func (wc *WrappedClient) beginProcessing(cc concurrentCuckoo, reprocess, integrity bool) (*sync.WaitGroup, chan<- *ItemGraph) {
	wg := new(sync.WaitGroup)
	ch := make(chan *ItemGraph)

	const workers = 2 // TODO: Make configurable?
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for ig := range ch {
				if ig == nil {
					continue
				}
				_, err := wc.processItemGraph(ig, &recursiveState{
					timestamp:      time.Now(),
					reprocess:      reprocess,
					integrityCheck: integrity,
					seen:           make(map[*ItemGraph]int64),
					idmap:          make(map[string]int64),
					cuckoo:         cc,
				})
				if err != nil {
					log.Printf("[ERROR][%s/%s] Processing item graph: %v",
						wc.ds.ID, wc.acc.UserID, err)
				}
			}
		}(i)
	}

	return wg, ch
}

type recursiveState struct {
	timestamp      time.Time
	reprocess      bool
	integrityCheck bool
	seen           map[*ItemGraph]int64 // value is the item's row ID
	idmap          map[string]int64     // map an item's service ID to the row ID -- TODO: I don't love this... any better way?

	// the cuckoo filter pointer lives for
	// the duration of the entire operation;
	// it is often nil, but if it is set,
	// then the service-produced ID of each
	// item should be added to the filter so
	// that a prune can take place when the
	// entire operation is complete
	cuckoo concurrentCuckoo
}

func (wc *WrappedClient) processItemGraph(ig *ItemGraph, state *recursiveState) (int64, error) {
	// don't visit a node twice
	if igID, ok := state.seen[ig]; ok {
		return igID, nil
	}

	var igRowID int64

	if ig.Node == nil {
		// mark this node as visited
		state.seen[ig] = 0
	} else {
		// process root node
		var err error
		igRowID, err = wc.processSingleItemGraphNode(ig.Node, state)
		if err != nil {
			return 0, fmt.Errorf("processing node of item graph: %v", err)
		}

		// mark this node as visited
		state.seen[ig] = igRowID

		// map individual items to their row IDs
		state.idmap[ig.Node.ID()] = igRowID

		// process all connected nodes
		if ig.Edges != nil {
			for connectedIG, relations := range ig.Edges {
				// if node not yet visited, process it now
				connectedIGRowID, visited := state.seen[connectedIG]
				if !visited {
					connectedIGRowID, err = wc.processItemGraph(connectedIG, state)
					if err != nil {
						return igRowID, fmt.Errorf("processing node of item graph: %v", err)
					}
					state.seen[connectedIG] = connectedIGRowID
				}

				// store this item's ID for later
				state.idmap[connectedIG.Node.ID()] = connectedIGRowID

				// insert relations to this connected node into DB
				for _, rel := range relations {
					_, err = wc.tl.db.Exec(`INSERT INTO relationships
					(from_item_id, to_item_id, directed, label)
					VALUES (?, ?, ?, ?)`,
						igRowID, connectedIGRowID, !rel.Bidirectional, rel.Label)
					if err != nil {
						return igRowID, fmt.Errorf("storing item relationship: %v (from_item=%d to_item=%d directed=%t label=%v)",
							err, igRowID, connectedIGRowID, !rel.Bidirectional, rel.Label)
					}
				}
			}
		}
	}

	// process collections, if any
	for _, coll := range ig.Collections {
		// attach the item's row ID to each item in the collection
		// to speed up processing; we won't have to query the database
		// again for items that were already processed from the graph
		for i, it := range coll.Items {
			coll.Items[i].itemRowID = state.idmap[it.Item.ID()]
		}

		err := wc.processCollection(coll, state.timestamp)
		if err != nil {
			return 0, fmt.Errorf("processing collection: %v (original_id=%s)", err, coll.OriginalID)
		}
	}

	// process raw relations, if any
	for _, rr := range ig.Relations {
		// get each item's row ID from their data source item ID
		fromItemRowID, err := wc.itemRowIDFromOriginalID(rr.FromItemID)
		if err == sql.ErrNoRows {
			continue // item does not exist in timeline; skip this relation
		}
		if err != nil {
			return 0, fmt.Errorf("querying 'from' item row ID: %v", err)
		}
		toItemRowID, err := wc.itemRowIDFromOriginalID(rr.ToItemID)
		if err == sql.ErrNoRows {
			continue // item does not exist in timeline; skip this relation
		}
		if err != nil {
			return 0, fmt.Errorf("querying 'to' item row ID: %v", err)
		}

		// store the relation
		_, err = wc.tl.db.Exec(`INSERT INTO relationships
					(from_item_id, to_item_id, directed, label)
					VALUES (?, ?, ?, ?)`,
			fromItemRowID, toItemRowID, rr.Bidirectional, rr.Label)
		if err != nil {
			return 0, fmt.Errorf("storing raw item relationship: %v (from_item=%d to_item=%d directed=%t label=%v)",
				err, fromItemRowID, toItemRowID, !rr.Bidirectional, rr.Label)
		}
	}

	return igRowID, nil
}

func (wc *WrappedClient) processSingleItemGraphNode(it Item, state *recursiveState) (int64, error) {
	if itemID := it.ID(); itemID != "" && state.cuckoo.Filter != nil {
		state.cuckoo.Lock()
		state.cuckoo.InsertUnique([]byte(itemID))
		state.cuckoo.Unlock()
	}

	itemRowID, err := wc.storeItemFromService(it, state.timestamp, state.reprocess, state.integrityCheck)
	if err != nil {
		return itemRowID, err
	}

	// item was stored successfully, so now keep track of the item with the highest
	// (latest, last, etc.) timestamp, so that get-latest operations can be resumed
	// after interruption without creating gaps in the data that would never be
	// filled in otherwise except with a get-all...
	itemTS := it.Timestamp()
	wc.lastItemMu.Lock()
	if wc.lastItemTimestamp.IsZero() || wc.lastItemTimestamp.Before(itemTS) {
		wc.lastItemRowID = itemRowID
		wc.lastItemTimestamp = itemTS
	}
	wc.lastItemMu.Unlock()

	return itemRowID, nil
}

func (wc *WrappedClient) storeItemFromService(it Item, timestamp time.Time, reprocess, integrity bool) (int64, error) {
	if it == nil {
		return 0, nil
	}

	// process this item only one at a time
	itemOriginalID := it.ID()
	itemLockID := fmt.Sprintf("%s_%d_%s", wc.ds.ID, wc.acc.ID, itemOriginalID)
	itemLocks.Lock(itemLockID)
	defer itemLocks.Unlock(itemLockID)

	// if there is a data file, prepare to download it
	// and get its file name; but don't actually begin
	// downloading it until after it is in the DB, since
	// we need to know, if we encounter this item later,
	// whether it was downloaded successfully; if not,
	// like if the download was interrupted and we didn't
	// have a chance to clean up, we can overwrite any
	// existing file by that name.
	rc, err := it.DataFileReader()
	if err != nil {
		return 0, fmt.Errorf("getting item's data file content stream: %v", err)
	}
	if rc != nil {
		defer rc.Close()
	}

	// if the item is already in our DB, load it
	var ir ItemRow
	if itemOriginalID != "" {
		ir, err = wc.loadItemRow(wc.acc.ID, itemOriginalID)
		if err != nil {
			return 0, fmt.Errorf("checking for item in database: %v", err)
		}
		if ir.ID > 0 {
			// already have it

			if !wc.shouldProcessExistingItem(it, ir, reprocess, integrity) {
				return ir.ID, nil
			}

			// at this point, we will be replacing the existing
			// file, so move it temporarily as a safe measure,
			// and also because our filename-generator will not
			// allow a file to be overwritten, but we want to
			// replace the existing file in this case
			if ir.DataFile != nil && rc != nil {
				origFile := wc.tl.fullpath(*ir.DataFile)
				bakFile := wc.tl.fullpath(*ir.DataFile + ".bak")
				err = os.Rename(origFile, bakFile)
				if err != nil && !os.IsNotExist(err) {
					return 0, fmt.Errorf("temporarily moving data file: %v", err)
				}

				// if this function returns with an error,
				// restore the original file in case it was
				// partially written or something; otherwise
				// delete the old file altogether
				defer func() {
					if err == nil {
						err := os.Remove(bakFile)
						if err != nil && !os.IsNotExist(err) {
							log.Printf("[ERROR] Deleting data file backup: %v", err)
						}
					} else {
						err := os.Rename(bakFile, origFile)
						if err != nil && !os.IsNotExist(err) {
							log.Printf("[ERROR] Restoring original data file from backup: %v", err)
						}
					}
				}()
			}
		}
	}

	var dataFileName *string
	var datafile *os.File
	if rc != nil {
		datafile, dataFileName, err = wc.tl.openUniqueCanonicalItemDataFile(it, wc.ds.ID)
		if err != nil {
			return 0, fmt.Errorf("opening output data file: %v", err)
		}
		defer datafile.Close()
	}

	// prepare the item's DB row values
	err = wc.fillItemRow(&ir, it, timestamp, dataFileName)
	if err != nil {
		return 0, fmt.Errorf("assembling item for storage: %v", err)
	}

	// TODO: Insert modified time too, if edited locally?
	// TODO: On conflict, maybe we just want to ignore -- make this configurable...
	_, err = wc.tl.db.Exec(`INSERT INTO items
			(account_id, original_id, person_id, timestamp, stored,
				class, mime_type, data_text, data_file, data_hash, metadata,
				latitude, longitude)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (account_id, original_id) DO UPDATE
			SET person_id=?, timestamp=?, stored=?, class=?, mime_type=?, data_text=?,
				data_file=?, data_hash=?, metadata=?, latitude=?, longitude=?`,
		ir.AccountID, ir.OriginalID, ir.PersonID, ir.Timestamp.Unix(), ir.Stored.Unix(),
		ir.Class, ir.MIMEType, ir.DataText, ir.DataFile, ir.DataHash, ir.metaGob,
		ir.Latitude, ir.Longitude,
		ir.PersonID, ir.Timestamp.Unix(), ir.Stored.Unix(), ir.Class, ir.MIMEType, ir.DataText,
		ir.DataFile, ir.DataHash, ir.metaGob, ir.Latitude, ir.Longitude)
	if err != nil {
		return 0, fmt.Errorf("storing item in database: %v (item_id=%v)", err, ir.OriginalID)
	}

	// get the item's row ID (this works regardless of whether
	// the last query was an insert or an update)
	var itemRowID int64
	err = wc.tl.db.QueryRow(`SELECT id FROM items
		WHERE account_id=? AND original_id=? LIMIT 1`,
		ir.AccountID, ir.OriginalID).Scan(&itemRowID)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("getting item row ID: %v", err)
	}

	// if there is a data file, download it and compute its checksum;
	// then update the item's row in the DB with its name and checksum
	if rc != nil && dataFileName != nil {
		h := sha256.New()
		err := wc.tl.downloadItemFile(rc, datafile, h)
		if err != nil {
			return 0, fmt.Errorf("downloading data file: %v (item_id=%v)", err, itemRowID)
		}

		// now that download is complete, compute its hash
		dfHash := h.Sum(nil)
		b64hash := base64.StdEncoding.EncodeToString(dfHash)

		// if the exact same file (byte-for-byte) already exists,
		// delete this copy and reuse the existing one
		err = wc.tl.replaceWithExisting(dataFileName, b64hash, itemRowID)
		if err != nil {
			return 0, fmt.Errorf("replacing data file with identical existing file: %v", err)
		}

		// save the file's name and hash to confirm it was downloaded successfully
		_, err = wc.tl.db.Exec(`UPDATE items SET data_hash=? WHERE id=?`, // TODO: LIMIT 1...
			b64hash, itemRowID)
		if err != nil {
			log.Printf("[ERROR][%s/%s] Updating item's data file hash in DB: %v; cleaning up data file: %s (item_id=%d)",
				wc.ds.ID, wc.acc.UserID, err, datafile.Name(), itemRowID)
			os.Remove(wc.tl.fullpath(*dataFileName))
		}
	}

	return itemRowID, nil
}

func (wc *WrappedClient) shouldProcessExistingItem(it Item, dbItem ItemRow, reprocess, integrity bool) bool {
	// if integrity check is enabled and checksum mismatches, always reprocess
	if integrity && dbItem.DataFile != nil && dbItem.DataHash != nil {
		datafile, err := os.Open(wc.tl.fullpath(*dbItem.DataFile))
		if err != nil {
			log.Printf("[ERROR][%s/%s] Integrity check: opening existing data file: %v; reprocessing (item_id=%d)",
				wc.ds.ID, wc.acc.UserID, err, dbItem.ID)
			return true
		}
		defer datafile.Close()
		h := sha256.New()
		_, err = io.Copy(h, datafile)
		if err != nil {
			log.Printf("[ERROR][%s/%s] Integrity check: reading existing data file: %v; reprocessing (item_id=%d)",
				wc.ds.ID, wc.acc.UserID, err, dbItem.ID)
			return true
		}
		b64hash := base64.StdEncoding.EncodeToString(h.Sum(nil))
		if b64hash != *dbItem.DataHash {
			log.Printf("[ERROR][%s/%s] Integrity check: checksum mismatch: expected %s, got %s; reprocessing (item_id=%d)",
				wc.ds.ID, wc.acc.UserID, *dbItem.DataHash, b64hash, dbItem.ID)
			return true
		}
	}

	// if modified locally, do not overwrite changes
	if dbItem.Modified != nil {
		return false
	}

	// if a data file is expected, but no completed file exists
	// (i.e. its hash is missing), then reprocess to allow download
	// to complete successfully this time
	if dbItem.DataFile != nil && dbItem.DataHash == nil {
		return true
	}

	// if service reports hashes/etags and we see that it
	// has changed, reprocess
	if serviceHash := it.DataFileHash(); serviceHash != nil &&
		dbItem.Metadata != nil &&
		dbItem.Metadata.ServiceHash != nil &&
		!bytes.Equal(serviceHash, dbItem.Metadata.ServiceHash) {
		return true
	}

	// finally, if the user wants to reprocess anyway, then do so
	return reprocess
}

func (wc *WrappedClient) fillItemRow(ir *ItemRow, it Item, timestamp time.Time, canonicalDataFileName *string) error {
	// unpack the item's information into values to use in the row

	ownerID, ownerName := it.Owner()
	if ownerID == nil {
		ownerID = &wc.acc.UserID // assume current account
	}
	if ownerName == nil {
		empty := ""
		ownerName = &empty
	}
	person, err := wc.tl.getPerson(wc.ds.ID, *ownerID, *ownerName)
	if err != nil {
		return fmt.Errorf("getting person associated with item: %v", err)
	}

	txt, err := it.DataText()
	if err != nil {
		return fmt.Errorf("getting item text: %v", err)
	}

	loc, err := it.Location()
	if err != nil {
		return fmt.Errorf("getting item location data: %v", err)
	}
	if loc == nil {
		loc = new(Location) // avoid nil pointer dereference below
	}

	// metadata (optional) needs to be gob-encoded
	metadata, err := it.Metadata()
	if err != nil {
		return fmt.Errorf("getting item metadata: %v", err)
	}
	if serviceHash := it.DataFileHash(); serviceHash != nil {
		metadata.ServiceHash = serviceHash
	}
	var metaGob []byte
	if metadata != nil {
		metaGob, err = metadata.encode() // use special encoding method for massive space savings
		if err != nil {
			return fmt.Errorf("gob-encoding metadata: %v", err)
		}
	}

	ir.AccountID = wc.acc.ID
	ir.OriginalID = it.ID()
	ir.PersonID = person.ID
	ir.Timestamp = it.Timestamp()
	ir.Stored = timestamp
	ir.Class = it.Class()
	ir.MIMEType = it.DataFileMIMEType()
	ir.DataText = txt
	ir.DataFile = canonicalDataFileName
	ir.Metadata = metadata
	ir.metaGob = metaGob
	ir.Location = *loc

	return nil
}

func (wc *WrappedClient) processCollection(coll Collection, timestamp time.Time) error {
	_, err := wc.tl.db.Exec(`INSERT INTO collections
		(account_id, original_id, name) VALUES (?, ?, ?)
		ON CONFLICT (account_id, original_id)
		DO UPDATE SET name=?`,
		wc.acc.ID, coll.OriginalID, coll.Name,
		coll.Name)
	if err != nil {
		return fmt.Errorf("inserting collection: %v", err)
	}

	// get the collection's row ID, regardless of whether it was inserted or updated
	var collID int64
	err = wc.tl.db.QueryRow(`SELECT id FROM collections
			WHERE account_id=? AND original_id=? LIMIT 1`,
		wc.acc.ID, coll.OriginalID).Scan(&collID)
	if err != nil {
		return fmt.Errorf("getting existing collection's row ID: %v", err)
	}

	// now add all the items
	// (TODO: could batch this for faster inserts)
	for _, cit := range coll.Items {
		if cit.itemRowID == 0 {
			itID, err := wc.storeItemFromService(cit.Item, timestamp, false, false) // never reprocess or check integrity here
			if err != nil {
				return fmt.Errorf("adding item from collection to storage: %v", err)
			}
			cit.itemRowID = itID
		}

		_, err = wc.tl.db.Exec(`INSERT OR IGNORE INTO collection_items
			(item_id, collection_id, position)
			VALUES (?, ?, ?)`,
			cit.itemRowID, collID, cit.Position, cit.Position)
		if err != nil {
			return fmt.Errorf("adding item to collection: %v", err)
		}
	}

	return nil
}

func (wc *WrappedClient) loadItemRow(accountID int64, originalID string) (ItemRow, error) {
	var ir ItemRow
	var metadataGob []byte
	var ts, stored int64 // will convert from Unix timestamp
	var modified *int64
	err := wc.tl.db.QueryRow(`SELECT
			id, account_id, original_id, person_id, timestamp, stored,
			modified, class, mime_type, data_text, data_file, data_hash,
			metadata, latitude, longitude
		FROM items WHERE account_id=? AND original_id=? LIMIT 1`, accountID, originalID).Scan(
		&ir.ID, &ir.AccountID, &ir.OriginalID, &ir.PersonID, &ts, &stored,
		&modified, &ir.Class, &ir.MIMEType, &ir.DataText, &ir.DataFile, &ir.DataHash,
		&metadataGob, &ir.Latitude, &ir.Longitude)
	if err == sql.ErrNoRows {
		return ItemRow{}, nil
	}
	if err != nil {
		return ItemRow{}, fmt.Errorf("loading item: %v", err)
	}

	// the metadata is gob-encoded; decode it into the struct
	ir.Metadata = new(Metadata)
	err = ir.Metadata.decode(metadataGob)
	if err != nil {
		return ItemRow{}, fmt.Errorf("gob-decoding metadata: %v", err)
	}

	ir.Timestamp = time.Unix(ts, 0)
	ir.Stored = time.Unix(stored, 0)
	if modified != nil {
		modTime := time.Unix(*modified, 0)
		ir.Modified = &modTime
	}

	return ir, nil
}

// itemRowIDFromOriginalID returns an item's row ID from the ID
// associated with the data source of wc, along with its original
// item ID from that data source. If the item does not exist,
// sql.ErrNoRows will be returned.
func (wc *WrappedClient) itemRowIDFromOriginalID(originalID string) (int64, error) {
	var rowID int64
	err := wc.tl.db.QueryRow(`SELECT items.id
			FROM items, accounts
			WHERE items.original_id=?
				AND accounts.data_source_id=?
				AND items.account_id = accounts.id
			LIMIT 1`, originalID, wc.ds.ID).Scan(&rowID)
	return rowID, err
}

// itemLocks is used to ensure that an item
// is not processed twice at the same time.
var itemLocks = newMapMutex()
