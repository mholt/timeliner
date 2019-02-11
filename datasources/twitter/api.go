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
	ownerAccount, err := c.getOwnerAccountFromAPI(cleanedScreenName)
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

			// TODO: Is this the right finish criteria?
			if len(tweets) == 0 {
				return nil
			}

			for _, t := range tweets {
				skip, err := c.prepareTweet(&t, "api")
				if err != nil {
					return fmt.Errorf("preparing tweet: %v", err)
				}
				if skip {
					continue
				}

				c.processTweet(itemChan, t, "")
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

// nextPageOfTweetsFromAPI returns the next page of tweets starting at maxTweet
// and going for a full page or until minTweet, whichever comes first. Generally,
// iterating over this function will involve decreasing maxTweet and leaving
// minTweet the same, if set at all (maxTweet = "until", minTweet = "since").
// Either or both can be empty strings, for no boundaries. This function returns
// at least 0 tweets (signaling done, I think) or up to a full page of tweets.
func (c *Client) nextPageOfTweetsFromAPI(maxTweet, minTweet string) ([]tweet, error) {
	q := url.Values{
		"user_id":         {c.ownerAccount.id()}, // TODO
		"count":           {"200"},
		"tweet_mode":      {"extended"}, // https://developer.twitter.com/en/docs/tweets/tweet-updates
		"exclude_replies": {"false"},    // always include replies in case it's a self-reply; we can filter all others
		"include_rts":     {"false"},
	}
	if maxTweet != "" {
		q.Set("max_id", maxTweet)
	}
	if minTweet != "" {
		q.Set("since_id", minTweet)
	}
	if c.Retweets {
		q.Set("include_rts", "true")
	}

	resp, err := c.HTTPClient.Get("https://api.twitter.com/1.1/statuses/user_timeline.json?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("performing API request: %v", err)
	}
	defer resp.Body.Close()

	// TODO: handle HTTP errors, esp. rate limiting, a lot better
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}

	var tweets []tweet
	err = json.NewDecoder(resp.Body).Decode(&tweets)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %v", err)
	}

	return tweets, nil
}

func (c *Client) getOwnerAccountFromAPI(screenName string) (twitterAccount, error) {
	var ta twitterAccount

	q := url.Values{"screen_name": {screenName}}

	resp, err := c.HTTPClient.Get("https://api.twitter.com/1.1/users/show.json?" + q.Encode())
	if err != nil {
		return ta, fmt.Errorf("performing API request: %v", err)
	}
	defer resp.Body.Close()

	// TODO: handle HTTP errors, esp. rate limiting, a lot better
	if resp.StatusCode != http.StatusOK {
		return ta, fmt.Errorf("HTTP error: %s", resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(&ta)
	if err != nil {
		return ta, fmt.Errorf("reading response body: %v", err)
	}

	return ta, nil
}
