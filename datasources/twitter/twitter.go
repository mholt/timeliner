// Package twitter implements a Timeliner service for importing
// and downloading data from Twitter.
package twitter

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/mholt/archiver"
	"github.com/mholt/timeliner"
	cuckoo "github.com/seiflotfy/cuckoofilter"
)

// Service name and ID.
const (
	DataSourceName = "Twitter"
	DataSourceID   = "twitter"
)

var dataSource = timeliner.DataSource{
	ID:   DataSourceID,
	Name: DataSourceName,
	OAuth2: timeliner.OAuth2{
		ProviderID: "twitter",
	},
	RateLimit: timeliner.RateLimit{
		// from https://developer.twitter.com/en/docs/basics/rate-limits
		// with some leeway since it's actually a pretty generous limit
		RequestsPerHour: 5900,
	},
	NewClient: func(acc timeliner.Account) (timeliner.Client, error) {
		httpClient, err := acc.NewHTTPClient()
		if err != nil {
			return nil, err
		}
		return &Client{
			HTTPClient:     httpClient,
			acc:            acc,
			topLevelTweets: cuckoo.NewFilter(1000000),
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
	Retweets bool

	Replies bool // TODO: replies should include context, like the surrounding conversation, as part of the graph...
	// Threads bool // TODO: this requires more tweets, using the API

	HTTPClient *http.Client

	checkpoint checkpointInfo

	acc            timeliner.Account
	ownerAccount   twitterAccount
	topLevelTweets *cuckoo.Filter
}

// ListItems lists items from opt.Filename if specified, or from the API otherwise.
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	defer close(itemChan)

	if opt.Filename != "" {
		return c.getFromArchiveFile(itemChan, opt)
	}

	return c.getFromAPI(ctx, itemChan, opt)
}

func (c *Client) prepareTweet(t *tweet, source string) (skip bool, err error) {
	// mark whether this tweet came from the API or an export file
	t.source = source

	// tweets from an import file are presumed to all be from the account owner
	t.ownerAccount = c.ownerAccount

	// skip tweets we aren't interested in
	if !c.Retweets && t.isRetweet() {
		return true, nil
	}
	if !c.Replies && t.InReplyToUserIDStr != "" && t.InReplyToUserIDStr != t.ownerAccount.id() {
		// TODO: Replies should have more context, like what are we replying to, etc... the whole thread, even?
		// this option is about replies to tweets other than our own, which are like a continuation of one thought
		return true, nil
	}

	// parse Twitter's time string into an actual time value
	t.createdAtParsed, err = time.Parse("Mon Jan 2 15:04:05 -0700 2006", t.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("parsing created_at time: %v", err)
	}

	return false, nil
}

func (c *Client) processTweet(itemChan chan<- *timeliner.ItemGraph, t tweet, archiveFilename string) error {
	oneMediaItem := t.hasExactlyOneMediaItem()

	// only create a tweet item if it has text OR exactly one media item
	// (because we don't want an empty item; we process each media item
	// as a separate item, unless there's exactly 1, in which case we
	// in-line it into the tweet itself)
	var ig *timeliner.ItemGraph
	if t.text() != "" || !oneMediaItem {
		ig = timeliner.NewItemGraph(&t)
	}

	// process the media items attached to the tweet
	if t.ExtendedEntities != nil {
		var collItems []timeliner.CollectionItem

		for i, m := range t.ExtendedEntities.Media {
			m.parent = &t

			var dataFileName string
			if dfn := m.DataFileName(); dfn == nil || *dfn == "" {
				log.Printf("[ERROR][%s/%s] Tweet media has no data file name: %+v",
					DataSourceID, c.acc.UserID, m)
				continue
			} else {
				dataFileName = *dfn
			}

			switch t.source {
			case "archive":
				targetFileInArchive := path.Join("tweet_media", dataFileName)

				err := archiver.Walk(archiveFilename, func(f archiver.File) error {
					if f.Header.(zip.FileHeader).Name != targetFileInArchive {
						return nil
					}

					buf := new(bytes.Buffer)
					_, err := io.Copy(buf, f)
					if err != nil {
						return fmt.Errorf("copying item into memory: %v", err)
					}
					m.readCloser = timeliner.FakeCloser(buf)

					return archiver.ErrStopWalk
				})
				if err != nil {
					return fmt.Errorf("walking archive file %s in search of tweet media: %v",
						archiveFilename, err)
				}

			case "api":
				mediaURL := m.getURL()
				if m.Type == "photo" {
					mediaURL += ":orig" // get original file, with metadata
				}
				resp, err := http.Get(mediaURL)
				if err != nil {
					return fmt.Errorf("getting media resource %s: %v", m.MediaURLHTTPS, err)
				}
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("media resource returned HTTP status %s: %s", resp.Status, m.MediaURLHTTPS)
				}
				m.readCloser = resp.Body

			default:
				return fmt.Errorf("unrecognized source value: must be api or archive: %s", t.source)
			}

			if !oneMediaItem {
				if ig != nil {
					ig.Add(m, timeliner.RelAttached)
				}
				collItems = append(collItems, timeliner.CollectionItem{
					Item:     m,
					Position: i,
				})
			}
		}

		if len(collItems) > 0 {
			ig.Collections = append(ig.Collections, timeliner.Collection{
				OriginalID: "tweet_" + t.ID(),
				Items:      collItems,
			})
		}
	}

	// if we're using the API, go ahead and get the
	// 'parent' tweet to which this tweet is a reply
	if t.source == "api" && t.InReplyToStatusIDStr != "" {
		// TODO: link up replies when processing via the API
	}

	// send the tweet for processing
	if ig != nil {
		itemChan <- ig
	}

	// if this is a top-level tweet (i.e. not a reply), mark
	// it so that we can use it to get replies from our own
	// top level tweets, as they can be a continuation of thought
	if t.InReplyToStatusIDStr == "" {
		c.topLevelTweets.InsertUnique([]byte(t.TweetIDStr))
	}

	return nil
}

// Assuming checkpoints are short-lived (i.e. are resumed
// somewhat quickly, before the page tokens/cursors expire),
// we can just store the page tokens.
type checkpointInfo struct {
	LastTweetID string
}

// save records the checkpoint.
func (ch *checkpointInfo) save(ctx context.Context) {
	gobBytes, err := timeliner.MarshalGob(ch)
	if err != nil {
		log.Printf("[ERROR][%s] Encoding checkpoint: %v", DataSourceID, err)
	}
	timeliner.Checkpoint(ctx, gobBytes)
}

// load decodes the checkpoint.
func (ch *checkpointInfo) load(checkpointGob []byte) {
	if len(checkpointGob) == 0 {
		return
	}
	err := timeliner.UnmarshalGob(checkpointGob, ch)
	if err != nil {
		log.Printf("[ERROR][%s] Decoding checkpoint: %v", DataSourceID, err)
	}
}

// maxTweetID returns the higher of the two tweet IDs.
// Errors parsing the strings as integers are ignored.
// Empty string inputs are ignored so the other value
// will win automatically. If both are empty, an empty
// string is returned.
func maxTweetID(id1, id2 string) string {
	if id1 == "" {
		return id2
	}
	if id2 == "" {
		return id1
	}
	id1int, _ := strconv.ParseInt(id1, 10, 64)
	id2int, _ := strconv.ParseInt(id2, 10, 64)
	if id1int > id2int {
		return id1
	}
	return id2
}
