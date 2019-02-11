package twitter

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/mholt/archiver"
	"github.com/mholt/timeliner"
)

func (c *Client) getFromArchiveFile(itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	// load the user's account ID
	var err error
	c.ownerAccount, err = c.getOwnerAccountFromArchive(opt.Filename)
	if err != nil {
		return fmt.Errorf("unable to get user account ID: %v", err)
	}

	// first pass - add tweets to timeline
	err = c.processArchive(opt.Filename, itemChan, c.processTweet)
	if err != nil {
		return fmt.Errorf("processing tweets: %v", err)
	}

	// second pass - add tweet relationships to timeline
	err = c.processArchive(opt.Filename, itemChan, c.processReplyRelationFromArchive)
	if err != nil {
		return fmt.Errorf("processing tweets: %v", err)
	}

	return nil
}

func (c *Client) processArchive(archiveFilename string, itemChan chan<- *timeliner.ItemGraph, processFunc archiveProcessFn) error {
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

		err = c.processTweetsFromArchive(itemChan, f, archiveFilename, processFunc)
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

func (c *Client) processTweetsFromArchive(itemChan chan<- *timeliner.ItemGraph, f io.Reader,
	archiveFilename string, processFunc archiveProcessFn) error {

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

		skip, err := c.prepareTweet(&t, "archive")
		if err != nil {
			return fmt.Errorf("preparing tweet: %v", err)
		}
		if skip {
			continue
		}

		err = processFunc(itemChan, t, archiveFilename)
		if err != nil {
			return fmt.Errorf("processing tweet: %v", err)
		}
	}

	return nil
}

func (c *Client) processReplyRelationFromArchive(itemChan chan<- *timeliner.ItemGraph, t tweet, archiveFilename string) error {
	if t.InReplyToStatusIDStr == "" {
		// current tweet is not a reply, so no relationship to add
		return nil
	}
	if !c.topLevelTweets.Lookup([]byte(t.InReplyToStatusIDStr)) {
		// reply is not to a top-level tweet by self, so doesn't qualify for what we want
		return nil
	}

	ig := &timeliner.ItemGraph{
		Relations: []timeliner.RawRelation{
			{
				FromItemID: t.TweetIDStr,
				ToItemID:   t.InReplyToStatusIDStr,
				Relation:   timeliner.RelReplyTo,
			},
		},
	}

	itemChan <- ig

	return nil
}

func (c *Client) getOwnerAccountFromArchive(filename string) (twitterAccount, error) {
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

// archiveProcessFn is a function that processes a
// tweet from a Twitter export archive.
type archiveProcessFn func(itemChan chan<- *timeliner.ItemGraph, t tweet, archiveFilename string) error

// Variable definitions that are intended for
// use with JavaScript but which are of no use
// to us and would break the JSON parser.
const (
	tweetFilePreface   = "window.YTD.tweet.part0 ="
	accountFilePreface = "window.YTD.account.part0 ="
)
