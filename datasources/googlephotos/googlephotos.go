// Package googlephotos implements the Google Photos service
// using its API, documented at https://developers.google.com/photos/.
package googlephotos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mholt/timeliner"
)

// Data source name and ID.
const (
	DataSourceName = "Google Photos"
	DataSourceID   = "google_photos"

	apiBase = "https://photoslibrary.googleapis.com/v1"
)

var dataSource = timeliner.DataSource{
	ID:   DataSourceID,
	Name: DataSourceName,
	OAuth2: timeliner.OAuth2{
		ProviderID: "google",
		Scopes:     []string{"https://www.googleapis.com/auth/photoslibrary.readonly"},
	},
	RateLimit: timeliner.RateLimit{
		RequestsPerHour: 10000 / 24, // https://developers.google.com/photos/library/guides/api-limits-quotas
		BurstSize:       3,
	},
	NewClient: func(acc timeliner.Account) (timeliner.Client, error) {
		httpClient, err := acc.NewHTTPClient()
		if err != nil {
			return nil, err
		}
		return &Client{
			HTTPClient: httpClient,
			userID:     acc.UserID,
			checkpoint: checkpointInfo{mu: new(sync.Mutex)},
		}, nil
	},
}

func init() {
	err := timeliner.RegisterDataSource(dataSource)
	if err != nil {
		log.Fatal(err)
	}
}

// Client interacts with the Google Photos
// API. It requires an OAuth2-authorized
// HTTP client in order to work properly.
type Client struct {
	HTTPClient           *http.Client
	IncludeArchivedMedia bool

	userID     string
	checkpoint checkpointInfo
}

// ListItems lists items from the data source.
// opt.Timeframe precision is day-level at best.
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.ListingOptions) error {
	defer close(itemChan)

	if opt.Filename != "" {
		return c.listFromTakeoutArchive(ctx, itemChan, opt)
	}

	// load any previous checkpoint
	c.checkpoint.load(opt.Checkpoint)

	// get items and collections
	errChan := make(chan error)
	go func() {
		err := c.listItems(ctx, itemChan, opt.Timeframe)
		errChan <- err
	}()
	go func() {
		err := c.listCollections(ctx, itemChan, opt.Timeframe)
		errChan <- err
	}()

	// read exactly 2 error (or nil) values to ensure we
	// block precisely until the two listers are done
	var errs []string
	for i := 0; i < 2; i++ {
		err := <-errChan
		if err != nil {
			log.Printf("[ERROR][%s/%s] A listing goroutine errored: %v", DataSourceID, c.userID, err)
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("one or more errors: %s", strings.Join(errs, ", "))
	}

	return nil
}

func (c *Client) listItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph,
	timeframe timeliner.Timeframe) error {
	c.checkpoint.mu.Lock()
	pageToken := c.checkpoint.ItemsNextPage
	c.checkpoint.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			var err error
			pageToken, err = c.getItemsNextPage(itemChan, pageToken, timeframe)
			if err != nil {
				return fmt.Errorf("getting items on next page: %v", err)
			}
			if pageToken == "" {
				return nil
			}

			c.checkpoint.mu.Lock()
			c.checkpoint.ItemsNextPage = pageToken
			c.checkpoint.save(ctx)
			c.checkpoint.mu.Unlock()
		}
	}
}

func (c *Client) getItemsNextPage(itemChan chan<- *timeliner.ItemGraph,
	pageToken string, timeframe timeliner.Timeframe) (string, error) {
	reqBody := listMediaItemsRequest{
		PageSize:  100,
		PageToken: pageToken,
	}
	if timeframe.Since != nil || timeframe.Until != nil {
		reqBody.Filters = &listMediaItemsFilter{
			DateFilter: listMediaItemsDateFilter{
				Ranges: []listMediaItemsFilterRange{dateRange(timeframe)},
			},
			IncludeArchivedMedia: c.IncludeArchivedMedia,
		}
	}

	page, err := c.pageOfMediaItems(reqBody)
	if err != nil {
		return "", fmt.Errorf("requesting next page: %v", err)
	}

	for _, item := range page.MediaItems {
		itemChan <- &timeliner.ItemGraph{
			Node: item,
		}
	}

	return page.NextPageToken, nil
}

// listCollections lists media items by iterating each album. As
// of Jan. 2019, the Google Photos API does not allow searching
// media items with both an album ID and filters. Because this
// search is predicated on album ID, we cannot be constrained by
// a timeframe in this search.
//
// See https://developers.google.com/photos/library/reference/rest/v1/mediaItems/search.
func (c *Client) listCollections(ctx context.Context,
	itemChan chan<- *timeliner.ItemGraph, timeframe timeliner.Timeframe) error {
	c.checkpoint.mu.Lock()
	albumPageToken := c.checkpoint.AlbumsNextPage
	c.checkpoint.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			var err error
			albumPageToken, err = c.getAlbumsAndTheirItemsNextPage(itemChan, albumPageToken, timeframe)
			if err != nil {
				return err
			}
			if albumPageToken == "" {
				return nil
			}

			c.checkpoint.mu.Lock()
			c.checkpoint.AlbumsNextPage = albumPageToken
			c.checkpoint.save(ctx)
			c.checkpoint.mu.Unlock()
		}
	}
}

func (c *Client) getAlbumsAndTheirItemsNextPage(itemChan chan<- *timeliner.ItemGraph,
	pageToken string, timeframe timeliner.Timeframe) (string, error) {
	vals := url.Values{
		"pageToken": {pageToken},
		"pageSize":  {"50"},
	}

	var respBody listAlbums
	err := c.apiRequestWithRetry("GET", "/albums?"+vals.Encode(), nil, &respBody)
	if err != nil {
		return pageToken, err
	}

	for _, album := range respBody.Albums {
		err = c.getAlbumItems(itemChan, album, timeframe)
		if err != nil {
			return "", err
		}
	}

	return respBody.NextPageToken, nil
}

func (c *Client) getAlbumItems(itemChan chan<- *timeliner.ItemGraph, album gpAlbum, timeframe timeliner.Timeframe) error {
	var albumItemsNextPage string
	var counter int

	for {
		reqBody := listMediaItemsRequest{
			AlbumID:   album.ID,
			PageToken: albumItemsNextPage,
			PageSize:  100,
		}

		page, err := c.pageOfMediaItems(reqBody)
		if err != nil {
			return fmt.Errorf("listing album contents: %v", err)
		}

		// iterate each media item on this page of the album listing
		var items []timeliner.CollectionItem
		for _, it := range page.MediaItems {
			// since we cannot request items in an album and also filter
			// by timestamp, be sure to filter here; it means we still
			// have to iterate all items in all albums, but at least we
			// can just skip items that fall outside the timeframe...
			ts := it.Timestamp()
			if timeframe.Since != nil && ts.Before(*timeframe.Since) {
				continue
			}
			if timeframe.Until != nil && ts.After(*timeframe.Until) {
				continue
			}

			// otherwise, add this item to the album
			items = append(items, timeliner.CollectionItem{
				Item:     it,
				Position: counter,
			})
			counter++
		}

		// if any items remained after filtering,
		// process this album now
		if len(items) > 0 {
			ig := timeliner.NewItemGraph(nil)
			ig.Collections = append(ig.Collections, timeliner.Collection{
				OriginalID: album.ID,
				Name:       &album.Title,
				Items:      items,
			})
			itemChan <- ig
		}

		if page.NextPageToken == "" {
			return nil
		}

		albumItemsNextPage = page.NextPageToken
	}
}

func (c *Client) pageOfMediaItems(reqBody listMediaItemsRequest) (listMediaItems, error) {
	var respBody listMediaItems
	err := c.apiRequestWithRetry("POST", "/mediaItems:search", reqBody, &respBody)
	return respBody, err
}

func (c *Client) apiRequestWithRetry(method, endpoint string, reqBodyData, respInto interface{}) error {
	// do the request in a loop for controlled retries on error
	var err error
	const maxTries = 10
	for i := 0; i < maxTries; i++ {
		var resp *http.Response
		resp, err = c.apiRequest(method, endpoint, reqBodyData)
		if err != nil {
			log.Printf("[ERROR][%s/%s] Doing API request: >>> %v <<< - retrying... (attempt %d/%d)",
				DataSourceID, c.userID, err, i+1, maxTries)
			time.Sleep(10 * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			bodyText, err2 := ioutil.ReadAll(io.LimitReader(resp.Body, 1024*256))
			resp.Body.Close()

			if err2 == nil {
				err = fmt.Errorf("HTTP %d: %s: >>> %s <<<", resp.StatusCode, resp.Status, bodyText)
			} else {
				err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
			}

			// extra-long pause for rate limiting errors
			if resp.StatusCode == http.StatusTooManyRequests {
				log.Printf("[ERROR][%s/%s] Rate limited: HTTP %d: %s: %s - retrying in 35 seconds... (attempt %d/%d)",
					DataSourceID, c.userID, resp.StatusCode, resp.Status, bodyText, i+1, maxTries)
				time.Sleep(35 * time.Second)
				continue
			}

			// for any other error, wait a couple seconds and retry
			log.Printf("[ERROR][%s/%s] Bad API response: %v - retrying... (attempt %d/%d)",
				DataSourceID, c.userID, err, i+1, maxTries)
			time.Sleep(10 * time.Second)
			continue
		}

		// successful request; read the response body
		err = json.NewDecoder(resp.Body).Decode(&respInto)
		if err != nil {
			resp.Body.Close()
			err = fmt.Errorf("decoding JSON: %v", err)
			log.Printf("[ERROR][%s/%s] Reading API response: %v - retrying... (attempt %d/%d)",
				DataSourceID, c.userID, err, i+1, maxTries)
			time.Sleep(10 * time.Second)
			continue
		}

		// successful read; we're done here
		resp.Body.Close()
		break
	}

	return err
}

func (c *Client) apiRequest(method, endpoint string, reqBodyData interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if reqBodyData != nil {
		reqBodyBytes, err := json.Marshal(reqBodyData)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(reqBodyBytes)
	}

	req, err := http.NewRequest(method, apiBase+endpoint, reqBody)
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.HTTPClient.Do(req)
}

func dateRange(timeframe timeliner.Timeframe) listMediaItemsFilterRange {
	var start, end filterDate
	if timeframe.Since == nil {
		start = filterDate{
			Day:   1,
			Month: 1,
			Year:  1,
		}
	} else {
		since := timeframe.Since.Add(24 * time.Hour) // to account for day precision
		start = filterDate{
			Day:   since.Day(),
			Month: int(since.Month()),
			Year:  since.Year(),
		}
	}
	if timeframe.Until == nil {
		end = filterDate{
			Day:   31,
			Month: 12,
			Year:  9999,
		}
	} else {
		timeframe.Until.Add(-24 * time.Hour) // to account for day precision
		end = filterDate{
			Day:   timeframe.Until.Day(),
			Month: int(timeframe.Until.Month()),
			Year:  timeframe.Until.Year(),
		}
	}
	return listMediaItemsFilterRange{
		StartDate: start,
		EndDate:   end,
	}
}

// Assuming checkpoints are short-lived (i.e. are resumed
// somewhat quickly, before the page tokens/cursors expire),
// we can just store the page tokens.
type checkpointInfo struct {
	ItemsNextPage  string
	AlbumsNextPage string
	mu             *sync.Mutex
}

// save records the checkpoint. It is NOT thread-safe,
// so calls to this must be protected by a mutex.
func (ch *checkpointInfo) save(ctx context.Context) {
	gobBytes, err := timeliner.MarshalGob(ch)
	if err != nil {
		log.Printf("[ERROR][%s] Encoding checkpoint: %v", DataSourceID, err)
	}
	timeliner.Checkpoint(ctx, gobBytes)
}

// load decodes the checkpoint. It is NOT thread-safe,
// so calls to this must be protected by a mutex.
func (ch *checkpointInfo) load(checkpointGob []byte) {
	if len(checkpointGob) == 0 {
		return
	}
	err := timeliner.UnmarshalGob(checkpointGob, ch)
	if err != nil {
		log.Printf("[ERROR][%s] Decoding checkpoint: %v", DataSourceID, err)
	}
}
