package timeliner

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"hash"
	"io"
	"log"
	mathrand "math/rand"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// downloadItemFile ... TODO.
func (t *Timeline) downloadItemFile(src io.ReadCloser, dest *os.File, h hash.Hash) error {
	if src == nil {
		return fmt.Errorf("missing reader with which to download file")
	}
	if dest == nil {
		return fmt.Errorf("missing file to download into")
	}

	// TODO: What if file already exists on disk (byte-for-byte)? - i.e. data_hash in DB has a duplicate

	// give the hasher a copy of the file bytes
	tr := io.TeeReader(src, h)

	if _, err := io.Copy(dest, tr); err != nil {
		os.Remove(dest.Name())
		return fmt.Errorf("copying contents: %v", err)
	}
	if err := dest.Sync(); err != nil {
		os.Remove(dest.Name())
		return fmt.Errorf("syncing file: %v", err)
	}

	// TODO: If mime type is photo or video, extract most important EXIF data and return it for storage in DB?

	return nil
}

// makeUniqueCanonicalItemDataFileName returns an available
// (non-overwriting) filename for the item's data file, starting
// with its plain, canonical data file name, then improvising
// and making unique if necessary. If there is no error, the
// return value is always a usable data file name.
// TODO: fix godoc
func (t *Timeline) openUniqueCanonicalItemDataFile(it Item, dataSourceID string) (*os.File, *string, error) {
	if dataSourceID == "" {
		return nil, nil, fmt.Errorf("missing service ID")
	}

	dir := t.canonicalItemDataFileDir(it, dataSourceID)

	err := os.MkdirAll(t.fullpath(dir), 0700)
	if err != nil {
		return nil, nil, fmt.Errorf("making directory for data file: %v", err)
	}

	tryPath := path.Join(dir, t.canonicalItemDataFileName(it, dataSourceID))
	lastAppend := path.Ext(tryPath)

	for i := 0; i < 100; i++ {
		fullFilePath := t.fullpath(filepath.FromSlash(tryPath))

		f, err := os.OpenFile(fullFilePath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
		if os.IsExist(err) {
			ext := path.Ext(tryPath)
			tryPath = strings.TrimSuffix(tryPath, lastAppend)
			lastAppend = fmt.Sprintf("_%d%s", i+1, ext) // start at 1, but actually 2 because existing file is "1"
			tryPath += lastAppend

			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("creating data file: %v", err)
		}

		return f, &tryPath, nil
	}

	return nil, nil, fmt.Errorf("unable to find available filename for item: %s", tryPath)
}

// canonicalItemDataFileName returns the plain, canonical name of the
// data file for the item. Canonical data file names are relative to
// the base storage (repo) path (i.e. the folder of the DB file). This
// function does no improvising in case of a name missing from the item,
// nor does it do uniqueness checks. If the item does not have enough
// information to generate a deterministic file name, the returned path
// will end with a trailing slash (i.e. the path's last component empty).
// Things considered deterministic for filename construction include the
// item's filename, the item's original ID, and its timestamp.
// TODO: fix godoc
func (t *Timeline) canonicalItemDataFileName(it Item, dataSourceID string) string {
	// ideally, the filename is simply the one provided with the item
	var filename string
	if fname := it.DataFileName(); fname != nil {
		filename = t.safePathComponent(*fname)
	}

	// otherwise, try a filename based on the item's ID
	if filename == "" {
		if itemOriginalID := it.ID(); itemOriginalID != "" {
			filename = fmt.Sprintf("item_%s", itemOriginalID)
		}
	}

	// otherwise, try a filename based on the item's timestamp
	ts := it.Timestamp()
	if filename == "" && !ts.IsZero() {
		filename = ts.Format("2006_01_02_150405")
	}

	// otherwise, out of options; revert to a random string
	// since no deterministic filename is available
	if filename == "" {
		filename = randomString(24, false)
	}

	// shorten the name if needed (thanks for everything, Windows)
	return t.ensureDataFileNameShortEnough(filename)
}

func (t *Timeline) canonicalItemDataFileDir(it Item, dataSourceID string) string {
	ts := it.Timestamp()
	if ts.IsZero() {
		ts = time.Now()
	}

	if dataSourceID == "" {
		dataSourceID = "unknown_service"
	}

	// use "/" separators and adjust for the OS
	// path separator when accessing disk
	return path.Join("data",
		fmt.Sprintf("%04d", ts.Year()),
		fmt.Sprintf("%02d", ts.Month()),
		t.safePathComponent(dataSourceID))
}

func (t *Timeline) ensureDataFileNameShortEnough(filename string) string {
	// thanks for nothing, Windows
	if len(filename) > 250 {
		ext := path.Ext(filename)
		if len(ext) > 20 { // arbitrary and unlikely, but just in case
			ext = ext[:20]
		}
		filename = filename[:250-len(ext)]
		filename += ext
	}
	return filename
}

// func ensureDataFileNameUnique(canonicalDataFileName string, maxTries int) (string, error) {
// 	if maxTries < 1 {
// 		panic("maxTries must be at least 1")
// 	}
// 	lastAppend := path.Ext(canonicalDataFileName)
// 	for i := 0; i < maxTries; i++ {
// 		if !datafileExists(canonicalDataFileName) {
// 			return canonicalDataFileName, nil
// 		}
// 		ext := path.Ext(canonicalDataFileName)
// 		canonicalDataFileName = strings.TrimSuffix(canonicalDataFileName, lastAppend)
// 		lastAppend = fmt.Sprintf("_%d%s", i+2, ext) // start at 1, but actually 2 because first file is "1"
// 		canonicalDataFileName += lastAppend
// 	}
// 	return "", fmt.Errorf("could not find an available filename for %s in %d iterations",
// 		canonicalDataFileName, maxTries)
// }

// TODO/NOTE: If changing a file name, all items with same data_hash must also be updated to use same file name
func (t *Timeline) replaceWithExisting(canonical *string, checksumBase64 string, itemRowID int64) error {
	if canonical == nil || *canonical == "" || checksumBase64 == "" {
		return fmt.Errorf("missing data filename and/or hash of contents")
	}

	var existingDatafile *string
	err := t.db.QueryRow(`SELECT data_file FROM items
		WHERE data_hash = ? AND id != ? LIMIT 1`,
		checksumBase64, itemRowID).Scan(&existingDatafile)
	if err == sql.ErrNoRows {
		return nil // file is unique; carry on
	}
	if err != nil {
		return fmt.Errorf("querying DB: %v", err)
	}

	// file is a duplicate!

	if existingDatafile == nil {
		// ... that's weird, how's this possible? it has a hash but no file name recorded
		return fmt.Errorf("item with matching hash is missing data file name; hash: %s", checksumBase64)
	}

	// ensure the existing file is still the same
	h := sha256.New()
	f, err := os.Open(t.fullpath(*existingDatafile))
	if err != nil {
		return fmt.Errorf("opening existing file: %v", err)
	}
	defer f.Close()

	_, err = io.Copy(h, f)
	if err != nil {
		return fmt.Errorf("checking file integrity: %v", err)
	}

	existingFileHash := h.Sum(nil)
	b64ExistingFileHash := base64.StdEncoding.EncodeToString(existingFileHash)

	// if the existing file was modified; restore it with
	// what we just downloaded, which presumably succeeded
	if checksumBase64 != b64ExistingFileHash {
		log.Printf("[INFO] Restoring modified data file: %s was '%s' but is now '%s'",
			*existingDatafile, checksumBase64, existingFileHash)
		err := os.Rename(t.fullpath(*canonical), t.fullpath(*existingDatafile))
		if err != nil {
			return fmt.Errorf("replacing modified data file: %v", err)
		}
	}

	// everything checks out; delete the newly-downloaded file
	// and use the existing file instead of duplicating it
	err = os.Remove(t.fullpath(*canonical))
	if err != nil {
		return fmt.Errorf("removing duplicate data file: %v", err)
	}

	canonical = existingDatafile

	return nil
}

// randomString returns a string of n random characters.
// It is not even remotely secure or a proper distribution.
// But it's good enough for some things. It excludes certain
// confusing characters like I, l, 1, 0, O, etc. If sameCase
// is true, then uppercase letters are excluded.
func randomString(n int, sameCase bool) string {
	if n <= 0 {
		return ""
	}
	dict := []byte("abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRTUVWXY23456789")
	if sameCase {
		dict = []byte("abcdefghijkmnpqrstuvwxyz0123456789")
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = dict[mathrand.Int63()%int64(len(dict))]
	}
	return string(b)
}

func (t *Timeline) fullpath(canonicalDatafileName string) string {
	return filepath.Join(t.repoDir, filepath.FromSlash(canonicalDatafileName))
}

func (t *Timeline) datafileExists(canonicalDatafileName string) bool {
	_, err := os.Stat(t.fullpath(canonicalDatafileName))
	return !os.IsNotExist(err)
}

func (t *Timeline) safePathComponent(s string) string {
	s = safePathRE.ReplaceAllLiteralString(s, "")
	s = strings.Replace(s, "..", "", -1)
	if s == "." {
		s = ""
	}
	return s
}

// safePathRER matches any undesirable characters in a filepath.
// Note that this allows dots, so you'll have to strip ".." manually.
var safePathRE = regexp.MustCompile(`[^\w.-]`)
