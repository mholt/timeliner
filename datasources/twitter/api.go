package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mholt/timeliner"
)

func (c *Client) getFromAPI(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	// load any previous checkpoint
	c.checkpoint.load(opt.Checkpoint)

	// get account owner information
	cleanedScreenName := strings.TrimPrefix(c.acc.UserID, "@")
	ownerAccount, err := c.getAccountFromAPI(cleanedScreenName, "")
	if err != nil {
		return fmt.Errorf("getting user account information for @%s: %v", cleanedScreenName, err)
	}
	c.ownerAccount = ownerAccount

	// get the starting bounds of this operation
	var maxTweet, minTweet string
	if opt.Timeframe.SinceItemID != nil {
		minTweet = *opt.Timeframe.SinceItemID
	}
	if c.checkpoint.LastTweetID != "" {
		// by default, start off at the last checkpoint
		maxTweet = c.checkpoint.LastTweetID
		if opt.Timeframe.UntilItemID != nil {
			// if both a timeframe UntilItemID and a checkpoint are set,
			// we will choose the one with a tweet ID that is higher,
			// meaning more recent, to avoid potentially skipping
			// a chunk of the timeline
			maxTweet = maxTweetID(c.checkpoint.LastTweetID, *opt.Timeframe.UntilItemID)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			tweets, err := c.nextPageOfTweetsFromAPI(maxTweet, minTweet)
			if err != nil {
				return fmt.Errorf("getting next page of tweets: %v", err)
			}

			// we are done when there are no more tweets
			if len(tweets) == 0 {
				return nil
			}

			for _, t := range tweets {
				err = c.processTweetFromAPI(t, itemChan)
				if err != nil {
					return fmt.Errorf("processing tweet from API: %v", err)
				}
			}

			// since max_id is inclusive, subtract 1 from the tweet ID
			// https://developer.twitter.com/en/docs/tweets/timelines/guides/working-with-timelines
			nextTweetID := tweets[len(tweets)-1].TweetID - 1
			c.checkpoint.LastTweetID = strconv.FormatInt(int64(nextTweetID), 10)
			c.checkpoint.save(ctx)

			// decrease maxTweet to get the next page on next iteration
			maxTweet = c.checkpoint.LastTweetID
		}
	}
}

func (c *Client) processTweetFromAPI(t tweet, itemChan chan<- *timeliner.ItemGraph) error {
	skip, err := c.prepareTweet(&t, "api")
	if err != nil {
		return fmt.Errorf("preparing tweet: %v", err)
	}
	if skip {
		return nil
	}

	ig, err := c.makeItemGraphFromTweet(t, "")
	if err != nil {
		return fmt.Errorf("processing tweet %s: %v", t.ID(), err)
	}

	// send the tweet for processing
	if ig != nil {
		itemChan <- ig
	}

	return nil
}

// nextPageOfTweetsFromAPI returns the next page of tweets starting at maxTweet
// and going for a full page or until minTweet, whichever comes first. Generally,
// iterating over this function will involve decreasing maxTweet and leaving
// minTweet the same, if set at all (maxTweet = "until", minTweet = "since").
// Either or both can be empty strings, for no boundaries. This function returns
// at least 0 tweets (signaling done, I think) or up to a full page of tweets.
func (c *Client) nextPageOfTweetsFromAPI(maxTweet, minTweet string) ([]tweet, error) {
	q := url.Values{
		"user_id":         {c.ownerAccount.id()},
		"count":           {"200"},
		"tweet_mode":      {"extended"}, // https://developer.twitter.com/en/docs/tweets/tweet-updates
		"exclude_replies": {"false"},    // always include replies in case it's a self-reply; we can filter all others
		"include_rts":     {"false"},
	}
	if c.Retweets {
		q.Set("include_rts", "true")
	}
	if maxTweet != "" {
		q.Set("max_id", maxTweet)
	}
	if minTweet != "" {
		q.Set("since_id", minTweet)
	}
	u := "https://api.twitter.com/1.1/statuses/user_timeline.json?" + q.Encode()

	resp, err := c.HTTPClient.Get(u)
	if err != nil {
		return nil, fmt.Errorf("performing API request: %v", err)
	}
	defer resp.Body.Close()

	// TODO: handle HTTP errors, esp. rate limiting, a lot better
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %s: %s", u, resp.Status)
	}

	var tweets []tweet
	err = json.NewDecoder(resp.Body).Decode(&tweets)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %v", err)
	}

	return tweets, nil
}

// getAccountFromAPI gets the account information for either
// screenName, if set, or accountID, if set. Set only one;
// leave the other argument empty string.
func (c *Client) getAccountFromAPI(screenName, accountID string) (twitterAccount, error) {
	var ta twitterAccount

	q := make(url.Values)
	if screenName != "" {
		q.Set("screen_name", screenName)
	} else if accountID != "" {
		q.Set("user_id", accountID)
	}

	u := "https://api.twitter.com/1.1/users/show.json?" + q.Encode()

	resp, err := c.HTTPClient.Get(u)
	if err != nil {
		return ta, fmt.Errorf("performing API request: %v", err)
	}
	defer resp.Body.Close()

	// TODO: handle HTTP errors, esp. rate limiting, a lot better
	if resp.StatusCode != http.StatusOK {
		return ta, fmt.Errorf("HTTP error: %s: %s", u, resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&ta)
	if err != nil {
		return ta, fmt.Errorf("reading response body: %v", err)
	}

	return ta, nil
}

func (c *Client) getTweetFromAPI(id string) (tweet, error) {
	var t tweet

	q := url.Values{
		"id":         {id},
		"tweet_mode": {"extended"}, // https://developer.twitter.com/en/docs/tweets/tweet-updates
	}
	u := "https://api.twitter.com/1.1/statuses/show.json?" + q.Encode()

	resp, err := c.HTTPClient.Get(u)
	if err != nil {
		return t, fmt.Errorf("performing API request: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		// this is okay, because the tweet may simply have been deleted,
		// and we skip empty tweets anyway
		fallthrough
	case http.StatusForbidden:
		// this happens when the author's account is suspended
		return t, nil
	case http.StatusOK:
		break
	default:
		// TODO: handle HTTP errors, esp. rate limiting, a lot better
		return t, fmt.Errorf("HTTP error: %s: %s", u, resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&t)
	if err != nil {
		return t, fmt.Errorf("reading response body: %v", err)
	}

	return t, nil
}
