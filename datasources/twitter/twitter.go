// Package twitter implements a Timeliner service for importing
// and downloading data from Twitter.
package twitter

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path"
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
	NewClient: func(acc timeliner.Account) (timeliner.Client, error) {
		return &Client{
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

	acc timeliner.Account

	ownerAccount twitterAccount

	topLevelTweets *cuckoo.Filter
}

// ListItems lists items from opt.Filename. TODO: support API too
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	defer close(itemChan)

	// TODO: integrate with the API too
	if opt.Filename == "" {
		return fmt.Errorf("filename is required")
	}

	// load the user's account ID
	var err error
	c.ownerAccount, err = c.getOwnerAccount(opt.Filename)
	if err != nil {
		return fmt.Errorf("unable to get user account ID: %v", err)
	}

	// first pass - add tweets to timeline
	err = c.processArchive(opt.Filename, itemChan, c.processTweet)
	if err != nil {
		return fmt.Errorf("processing tweets: %v", err)
	}

	// second pass - add tweet relationships to timeline
	err = c.processArchive(opt.Filename, itemChan, c.processReplyRelation)
	if err != nil {
		return fmt.Errorf("processing tweets: %v", err)
	}

	return nil
}

func (c *Client) processArchive(archiveFilename string, itemChan chan<- *timeliner.ItemGraph, processFunc processFn) error {
	err := archiver.Walk(archiveFilename, func(f archiver.File) error {
		defer f.Close()
		if f.Name() != "tweet.js" {
			return nil
		}

		// consume non-JSON preface (JavaScript variable definition)
		err := stripPreface(f, tweetFilePreface)
		if err != nil {
			return fmt.Errorf("reading tweet file preface: %v", err)
		}

		err = c.processTweets(itemChan, f, archiveFilename, processFunc)
		if err != nil {
			return fmt.Errorf("processing tweet file: %v", err)
		}

		return archiver.ErrStopWalk
	})
	if err != nil {
		return fmt.Errorf("walking archive file %s: %v", archiveFilename, err)
	}

	return nil
}

func (c *Client) processTweets(itemChan chan<- *timeliner.ItemGraph, f io.Reader,
	archiveFilename string, processFunc processFn) error {

	dec := json.NewDecoder(f)

	// read array opening bracket '['
	_, err := dec.Token()
	if err != nil {
		return fmt.Errorf("decoding opening token: %v", err)
	}

	for dec.More() {
		var t tweet
		err := dec.Decode(&t)
		if err != nil {
			return fmt.Errorf("decoding tweet element: %v", err)
		}

		// tweets from an import file are presumed to all be from the account owner
		t.ownerAccount = c.ownerAccount

		// skip tweets we aren't interested in
		if !c.Retweets && t.isRetweet() {
			continue // retweets
		}
		if !c.Replies && t.InReplyToUserID != "" && t.InReplyToUserID != t.ownerAccount.AccountID {
			// TODO: Replies should have more context, like what are we replying to, etc... the whole thread, even?
			// this option is about replies to tweets other than our own, which are like a continuation of one thought
			continue // replies
		}

		// parse Twitter's time string into an actual time value
		t.createdAtParsed, err = time.Parse("Mon Jan 2 15:04:05 -0700 2006", t.CreatedAt)
		if err != nil {
			return fmt.Errorf("parsing created_at time: %v", err)
		}

		err = processFunc(itemChan, t, archiveFilename)
		if err != nil {
			return fmt.Errorf("processing tweet: %v", err)
		}
	}

	return nil
}

func (c *Client) processTweet(itemChan chan<- *timeliner.ItemGraph, t tweet, archiveFilename string) error {
	oneMediaItem := t.hasExactlyOneMediaItem()

	// only create a tweet item if it has text OR exactly one media item
	// (because we don't want an empty item; we process each media item
	// as a separate item, unless there's exactly 1, in which case we
	// in-line it into the tweet itself)
	var ig *timeliner.ItemGraph
	if t.FullText != "" || !oneMediaItem {
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
				m.reader = buf

				if !oneMediaItem {
					if ig != nil {
						ig.Add(m, timeliner.RelAttached)
					}
					collItems = append(collItems, timeliner.CollectionItem{
						Item:     m,
						Position: i,
					})
				}

				return archiver.ErrStopWalk
			})
			if err != nil {
				return fmt.Errorf("walking archive file %s in search of tweet media: %v",
					archiveFilename, err)
			}

		}

		if len(collItems) > 0 {
			ig.Collections = append(ig.Collections, timeliner.Collection{
				OriginalID: "tweet_" + t.ID(),
				Items:      collItems,
			})
		}
	}

	// send the tweet for processing
	if ig != nil {
		itemChan <- ig
	}

	// if this is a top-level tweet (i.e. not a reply), mark
	// it so that we can use it to get replies from our own
	// top level tweets, as they can be a continuation of thought
	if t.InReplyToStatusID == "" {
		c.topLevelTweets.InsertUnique([]byte(t.TweetID))
	}

	return nil
}

func (c *Client) processReplyRelation(itemChan chan<- *timeliner.ItemGraph, t tweet, archiveFilename string) error {
	if t.InReplyToStatusID == "" {
		// current tweet is not a reply, so no relationship to add
		return nil
	}
	if !c.topLevelTweets.Lookup([]byte(t.InReplyToStatusID)) {
		// reply is not to a top-level tweet by self, so doesn't qualify for what we want
		return nil
	}

	ig := &timeliner.ItemGraph{
		Relations: []timeliner.RawRelation{
			{
				FromItemID: t.TweetID,
				ToItemID:   t.InReplyToStatusID,
				Relation:   timeliner.RelReplyTo,
			},
		},
	}

	itemChan <- ig

	return nil
}

func (c *Client) getOwnerAccount(filename string) (twitterAccount, error) {
	var ta twitterAccount
	err := archiver.Walk(filename, func(f archiver.File) error {
		defer f.Close()
		if f.Name() != "account.js" {
			return nil
		}

		// consume non-JSON preface (JavaScript variable definition)
		err := stripPreface(f, accountFilePreface)
		if err != nil {
			return fmt.Errorf("reading account file preface: %v", err)
		}

		var accFile twitterAccountFile
		err = json.NewDecoder(f).Decode(&accFile)
		if err != nil {
			return fmt.Errorf("decoding account file: %v", err)
		}
		if len(accFile) == 0 {
			return fmt.Errorf("account file was empty")
		}

		ta = accFile[0].Account

		return archiver.ErrStopWalk
	})
	return ta, err
}

func stripPreface(f io.Reader, preface string) error {
	buf := make([]byte, len(preface))
	_, err := io.ReadFull(f, buf)
	return err
}

// processFn is a function that processes a tweet.
type processFn func(itemChan chan<- *timeliner.ItemGraph, t tweet, archiveFilename string) error

// Variable definitions that are intended for
// use with JavaScript but which are of no use
// to us and would break the JSON parser.
const (
	tweetFilePreface   = "window.YTD.tweet.part0 ="
	accountFilePreface = "window.YTD.account.part0 ="
)
