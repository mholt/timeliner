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
	"net/url"
	"path"
	"regexp"
	"strconv"
	"time"

	"github.com/mholt/archiver/v3"
	"github.com/mholt/timeliner"
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
			HTTPClient:    httpClient,
			acc:           acc,
			otherAccounts: make(map[string]twitterAccount),
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
	Retweets bool // whether to include retweets
	Replies  bool // whether to include replies to tweets that are not our own; i.e. are not a continuation of thought

	HTTPClient *http.Client

	checkpoint checkpointInfo

	acc           timeliner.Account
	ownerAccount  twitterAccount
	otherAccounts map[string]twitterAccount // keyed by user/account ID
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

	// set the owner account information; this has to be done differently
	// depending on the source (it's not embedded in the archive's tweets...)
	switch t.source {
	case "archive":
		t.ownerAccount = c.ownerAccount
	case "api":
		if t.User != nil {
			if t.User.UserIDStr == c.ownerAccount.id() {
				// tweet author is the owner of the account - awesome
				t.ownerAccount = c.ownerAccount
			} else {
				// look up author's account info
				acc, ok := c.otherAccounts[t.User.UserIDStr]
				if !ok {
					acc, err = c.getAccountFromAPI("", t.User.UserIDStr)
					if err != nil {
						return false, fmt.Errorf("looking up tweet author's account information: %v", err)
					}
					// cache this for later
					if len(c.otherAccounts) > 2000 {
						for id := range c.otherAccounts {
							delete(c.otherAccounts, id)
							break
						}
					}
					c.otherAccounts[acc.IDStr] = acc
				}
				t.ownerAccount = acc
			}
		}
	default:
		return false, fmt.Errorf("unrecognized source: %s", t.source)
	}

	// skip empty tweets
	if t.isEmpty() {
		return true, nil
	}

	// skip tweets we aren't interested in
	if !c.Retweets && t.isRetweet() {
		return true, nil
	}
	if !c.Replies && t.InReplyToUserIDStr != "" && t.InReplyToUserIDStr != t.ownerAccount.id() {
		// TODO: Replies should have more context, like what are we replying to, etc... the whole thread, even?
		// this option is about replies to tweets other than our own (which are like a continuation of one thought)
		return true, nil
	}

	// parse Twitter's time string into an actual time value
	t.createdAtParsed, err = time.Parse("Mon Jan 2 15:04:05 -0700 2006", t.CreatedAt)
	if err != nil {
		return false, fmt.Errorf("parsing created_at time: %v", err)
	}

	return false, nil
}

func (c *Client) makeItemGraphFromTweet(t tweet, archiveFilename string) (*timeliner.ItemGraph, error) {
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
					return nil, fmt.Errorf("walking archive file %s in search of tweet media: %v",
						archiveFilename, err)
				}

			case "api":
				mediaURL := m.getURL()
				if m.Type == "photo" {
					mediaURL += ":orig" // get original file, with metadata
				}
				resp, err := http.Get(mediaURL)
				if err != nil {
					return nil, fmt.Errorf("getting media resource %s: %v", m.MediaURLHTTPS, err)
				}
				if resp.StatusCode != http.StatusOK {
					return nil, fmt.Errorf("media resource returned HTTP status %s: %s", resp.Status, m.MediaURLHTTPS)
				}
				m.readCloser = resp.Body

			default:
				return nil, fmt.Errorf("unrecognized source value: must be api or archive: %s", t.source)
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
		inReplyToTweet, err := c.getTweetFromAPI(t.InReplyToStatusIDStr)
		if err != nil {
			return nil, fmt.Errorf("getting tweet that this tweet (%s) is in reply to (%s): %v",
				t.ID(), t.InReplyToStatusIDStr, err)
		}
		skip, err := c.prepareTweet(&inReplyToTweet, "api")
		if err != nil {
			return nil, fmt.Errorf("preparing reply-parent tweet: %v", err)
		}
		if !skip {
			repIG, err := c.makeItemGraphFromTweet(inReplyToTweet, "")
			if err != nil {
				return nil, fmt.Errorf("making item from tweet that this tweet (%s) is in reply to (%s): %v",
					t.ID(), inReplyToTweet.ID(), err)
			}
			ig.Edges[repIG] = []timeliner.Relation{timeliner.RelReplyTo}
		}
	}

	// if this tweet embeds/quotes/links to other tweets,
	// we should establish those relationships as well
	if t.source == "api" && t.Entities != nil {
		for _, urlEnt := range t.Entities.URLs {
			embeddedTweetID := getLinkedTweetID(urlEnt.ExpandedURL)
			if embeddedTweetID == "" {
				continue
			}
			embeddedTweet, err := c.getTweetFromAPI(embeddedTweetID)
			if err != nil {
				return nil, fmt.Errorf("getting tweet that this tweet (%s) embeds (%s): %v",
					t.ID(), t.InReplyToStatusIDStr, err)
			}
			skip, err := c.prepareTweet(&embeddedTweet, "api")
			if err != nil {
				return nil, fmt.Errorf("preparing embedded tweet: %v", err)
			}
			if !skip {
				embIG, err := c.makeItemGraphFromTweet(embeddedTweet, "")
				if err != nil {
					return nil, fmt.Errorf("making item from tweet that this tweet (%s) embeds (%s): %v",
						t.ID(), embeddedTweet.ID(), err)
				}
				ig.Edges[embIG] = []timeliner.Relation{timeliner.RelQuotes}
			}
		}
	}

	return ig, nil
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

// getLinkedTweetID returns the ID of the tweet in
// a link to a tweet, for example:
// "https://twitter.com/foo/status/12345"
// returns "12345". If the tweet ID cannot be found
// or the URL does not match the right format,
// an empty string is returned.
func getLinkedTweetID(urlToTweet string) string {
	if !linkToTweetRE.MatchString(urlToTweet) {
		return ""
	}
	u, err := url.Parse(urlToTweet)
	if err != nil {
		return ""
	}
	return path.Base(u.Path)
}

var linkToTweetRE = regexp.MustCompile(`https?://twitter\.com/.*/status/[0-9]+`)
