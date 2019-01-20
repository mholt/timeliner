// Package facebook implements the Facebook service using
// the Graph API: https://developers.facebook.com/docs/graph-api
package facebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/mholt/timeliner"
)

// Data source name and ID.
const (
	DataSourceName = "Facebook"
	DataSourceID   = "facebook"
)

var dataSource = timeliner.DataSource{
	ID:   DataSourceID,
	Name: DataSourceName,
	OAuth2: timeliner.OAuth2{
		ProviderID: "facebook",
		Scopes: []string{
			"public_profile",
			"user_posts",
			"user_photos",
			"user_videos",
		},
	},
	RateLimit: timeliner.RateLimit{
		RequestsPerHour: 200,
		BurstSize:       3,
	},
	NewClient: func(acc timeliner.Account) (timeliner.Client, error) {
		httpClient, err := acc.NewHTTPClient()
		if err != nil {
			return nil, err
		}
		return &Client{
			httpClient: httpClient,
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

// Client implements the timeliner.Client interface.
type Client struct {
	httpClient *http.Client
	checkpoint checkpointInfo
}

// ListItems lists the items on the Facebook account.
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	defer close(itemChan)

	if opt.Filename != "" {
		return fmt.Errorf("importing from a file is not supported")
	}

	// load any previous checkpoint
	c.checkpoint.load(opt.Checkpoint)

	errChan := make(chan error)

	// TODO: events, comments (if possible), ...
	go func() {
		err := c.getFeed(ctx, itemChan, opt.Timeframe)
		errChan <- err
	}()
	go func() {
		err := c.getCollections(ctx, itemChan, opt.Timeframe)
		errChan <- err
	}()

	// read exactly 2 errors (or nils) because we
	// started 2 goroutines to do things
	var errs []string
	for i := 0; i < 2; i++ {
		err := <-errChan
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("one or more errors: %s", strings.Join(errs, ", "))
	}

	return nil
}

func (c *Client) getFeed(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, timeframe timeliner.Timeframe) error {
	c.checkpoint.mu.Lock()
	nextPageURL := c.checkpoint.ItemsNextPage
	c.checkpoint.mu.Unlock()

	var err error

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			nextPageURL, err = c.getFeedNextPage(itemChan, nextPageURL, timeframe)
			if err != nil {
				return err
			}
			if nextPageURL == nil {
				return nil
			}

			c.checkpoint.mu.Lock()
			c.checkpoint.ItemsNextPage = nextPageURL
			c.checkpoint.save(ctx)
			c.checkpoint.mu.Unlock()
		}
	}
}

func (c *Client) getFeedNextPage(itemChan chan<- *timeliner.ItemGraph,
	nextPageURL *string, timeframe timeliner.Timeframe) (*string, error) {

	nextPageURLStr := ""
	if nextPageURL != nil {
		nextPageURLStr = *nextPageURL
	}

	// TODO: When requesting a page using since&until, the results
	// need to be ordered differently because they start at the since,
	// and if we go "next" it goes the wrong direction; furthermore,
	// their "order" method is broken: https://developers.facebook.com/support/bugs/2231843933505877/
	// - that all needs to be figured out before we do much more here
	// with regards to timeframes
	user, err := c.requestPage(nextPageURLStr, timeframe)
	if err != nil {
		return nil, fmt.Errorf("requesting next page: %v", err)
	}

	// TODO: Refactor this into smaller functions...

	for _, post := range user.Feed.Data {

		ig := timeliner.NewItemGraph(post)

		for _, att := range post.Attachments.Data {
			if att.Type == "album" {

				// add all the items to a collection
				coll := timeliner.Collection{
					OriginalID: att.Target.ID,
					Name:       &att.Title,
				}

				for i, subatt := range att.Subattachments.Data {
					mediaID := subatt.Target.ID

					media, err := c.requestMedia(subatt.Type, mediaID)
					if err != nil {
						log.Printf("[ERROR] Getting media: %v", err)
						continue
					}

					coll.Items = append(coll.Items, timeliner.CollectionItem{
						Position: i,
						Item:     media,
					})

					ig.Add(media, timeliner.RelAttached)
				}

				ig.Collections = append(ig.Collections, coll)
			}
		}

		itemChan <- ig
	}

	return user.Feed.Paging.Next, nil
}

func (c *Client) requestPage(nextPageURL string, timeframe timeliner.Timeframe) (fbUser, error) {
	timeConstraint := fieldTimeConstraint(timeframe)
	nested := "{attachments,backdated_time,created_time,description,from,link,message,name,parent_id,place,status_type,type,with_tags}"

	v := url.Values{
		"fields": {"feed" + timeConstraint + nested},
		"order":  {"reverse_chronological"}, // TODO: see https://developers.facebook.com/support/bugs/2231843933505877/ (thankfully, reverse_chronological is their default for feed)
	}

	var user fbUser
	err := c.apiRequest("GET", "me?"+v.Encode(), nil, &user)
	return user, err
}

func (c *Client) requestMedia(mediaType, mediaID string) (*fbMedia, error) {
	if mediaType != "photo" && mediaType != "video" {
		return nil, fmt.Errorf("unknown media type: %s", mediaType)
	}

	fields := []string{"backdated_time", "created_time", "from", "id", "place", "updated_time"}
	switch mediaType {
	case "photo":
		fields = append(fields, "album", "images", "name", "name_tags")
	case "video":
		fields = append(fields, "description", "length", "source", "status", "title")
	}

	vals := url.Values{
		"fields": {strings.Join(fields, ",")},
	}
	endpoint := fmt.Sprintf("%s?%s", mediaID, vals.Encode())

	var media fbMedia
	err := c.apiRequest("GET", endpoint, nil, &media)
	if err != nil {
		return nil, err
	}
	media.fillFields(mediaType)

	return &media, nil
}

func (c *Client) getCollections(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, timeframe timeliner.Timeframe) error {
	c.checkpoint.mu.Lock()
	nextPageURL := c.checkpoint.AlbumsNextPage
	c.checkpoint.mu.Unlock()

	var err error
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			nextPageURL, err = c.getCollectionsNextPage(itemChan, nextPageURL, timeframe)
			if err != nil {
				return err
			}
			if nextPageURL == nil {
				return nil
			}

			c.checkpoint.mu.Lock()
			c.checkpoint.AlbumsNextPage = nextPageURL
			c.checkpoint.save(ctx)
			c.checkpoint.mu.Unlock()
		}
	}
}

func (c *Client) getCollectionsNextPage(itemChan chan<- *timeliner.ItemGraph,
	nextPageURL *string, timeframe timeliner.Timeframe) (*string, error) {

	var page fbMediaPage
	var err error
	if nextPageURL == nil {
		// get first page
		timeConstraint := fieldTimeConstraint(timeframe)
		v := url.Values{
			"fields": {"created_time,id,name,photos" + timeConstraint + "{album,backdated_time,created_time,from,id,images,updated_time,place,source}"},
		}
		v = qsTimeConstraint(v, timeframe)
		endpoint := fmt.Sprintf("me/albums?%s", v.Encode())
		err = c.apiRequest("GET", endpoint, nil, &page)
	} else {
		// get subsequent pages
		err = c.apiRequestFullURL("GET", *nextPageURL, nil, &page)
	}
	if err != nil {
		return nil, fmt.Errorf("requesting next page: %v", err)
	}
	nextPageURL = page.Paging.Next

	// iterate each album on this page
	for _, album := range page.Data {
		// make the collection object
		var coll timeliner.Collection
		coll.Name = &album.Name
		coll.OriginalID = album.MediaID

		// TODO...
		log.Println("ALBUM NAME:", *coll.Name)

		// add each photo to the collection, page by page
		if album.Photos != nil {
			var counter int

			for {
				log.Println("**** NEXT PAGE ****")
				for i := range album.Photos.Data {
					album.Photos.Data[i].fillFields("photo")
					log.Println("PHOTO:", album.Photos.Data[i].MediaID)

					coll.Items = append(coll.Items, timeliner.CollectionItem{
						Item:     &album.Photos.Data[i],
						Position: counter,
					})
					counter++
				}

				log.Println("ALBUM LEN:", len(coll.Items), *coll.Name)

				ig := timeliner.NewItemGraph(nil)
				ig.Collections = append(ig.Collections, coll)
				itemChan <- ig
				coll.Items = []timeliner.CollectionItem{}

				if album.Photos.Paging.Next == nil {
					break
				}

				log.Println("PHOTOS NEXT:", *album.Photos.Paging.Next)

				// request next page
				var nextPage *fbMediaPage
				err := c.apiRequestFullURL("GET", *album.Photos.Paging.Next, nil, &nextPage)
				if err != nil {
					return nil, fmt.Errorf("requesting next page of photos in album: %v", err)
				}
				album.Photos = nextPage
			}
		}

	}

	return page.Paging.Next, nil
}

func (c *Client) apiRequest(method, endpoint string, reqBodyData, respInto interface{}) error {
	return c.apiRequestFullURL(method, apiBase+endpoint, reqBodyData, respInto)
}

func (c *Client) apiRequestFullURL(method, fullURL string, reqBodyData, respInto interface{}) error {
	var reqBody io.Reader
	if reqBodyData != nil {
		reqBodyBytes, err := json.Marshal(reqBodyData)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(reqBodyBytes)
	}
	log.Println(fullURL)

	req, err := http.NewRequest(method, fullURL, reqBody)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("performing API request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&respInto)
	if err != nil {
		return fmt.Errorf("decoding JSON: %v", err)
	}

	return nil
}

// NOTE: for these timeConstraint functions... Facebook docs recommend either setting
// BOTH since and until or NEITHER "for consistent results" (but seems to work with
// just one anyways...?)
// see https://developers.facebook.com/docs/graph-api/using-graph-api#time
func fieldTimeConstraint(timeframe timeliner.Timeframe) string {
	var s string
	if timeframe.Since != nil {
		s += fmt.Sprintf(".since(%d)", timeframe.Since.Unix())
	}
	if timeframe.Until != nil {
		s += fmt.Sprintf(".until(%d)", timeframe.Until.Unix())
	}
	return s
}
func qsTimeConstraint(v url.Values, timeframe timeliner.Timeframe) url.Values {
	if timeframe.Since != nil {
		v.Set("since", fmt.Sprintf("%d", timeframe.Since.Unix()))
	}
	if timeframe.Until != nil {
		v.Set("until", fmt.Sprintf("%d", timeframe.Until.Unix()))
	}
	return v
}

type checkpointInfo struct {
	ItemsNextPage  *string
	AlbumsNextPage *string
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

// save records the checkpoint. It is NOT thread-safe,
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

type fbUser struct {
	Feed fbUserFeed `json:"feed"`
	ID   string     `json:"id"`
}

type fbUserFeed struct {
	Data   []fbPost `json:"data"`
	Paging fbPaging `json:"paging"`
}

type fbPaging struct {
	Previous *string    `json:"previous"`
	Next     *string    `json:"next"`
	Cursors  *fbCursors `json:"cursors,omitempty"`
}

type fbCursors struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type fbFrom struct {
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

type fbPlace struct {
	Name     string     `json:"name,omitempty"`
	Location fbLocation `json:"location,omitempty"`
	ID       string     `json:"id,omitempty"`
}

type fbLocation struct {
	City      string  `json:"city,omitempty"`
	Country   string  `json:"country,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	LocatedIn string  `json:"located_in,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Name      string  `json:"name,omitempty"`
	Region    string  `json:"region,omitempty"`
	State     string  `json:"state,omitempty"`
	Street    string  `json:"street,omitempty"`
	Zip       string  `json:"zip,omitempty"`
}

const apiBase = "https://graph.facebook.com/v3.2/"
