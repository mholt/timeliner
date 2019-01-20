package timeliner

import (
	"net/http"
	"time"
)

// RateLimit describes a rate limit.
type RateLimit struct {
	RequestsPerHour int
	BurstSize       int

	ticker *time.Ticker
	token  chan struct{}
}

// NewRateLimitedRoundTripper adds rate limiting to rt based on the rate
// limiting policy registered by the data source associated with acc.
func (acc Account) NewRateLimitedRoundTripper(rt http.RoundTripper) http.RoundTripper {
	rlKey := acc.DataSourceID + "_" + acc.UserID

	rl, ok := acc.t.rateLimiters[rlKey]

	if !ok && acc.ds.RateLimit.RequestsPerHour > 0 {
		secondsBetweenReqs := 60.0 / (float64(acc.ds.RateLimit.RequestsPerHour) / 60.0)
		reqInterval := time.Duration(secondsBetweenReqs) * time.Second

		rl.ticker = time.NewTicker(reqInterval)
		rl.token = make(chan struct{}, rl.BurstSize)

		for i := 0; i < cap(rl.token); i++ {
			rl.token <- struct{}{}
		}
		go func() {
			for range rl.ticker.C {
				rl.token <- struct{}{}
			}
		}()

		acc.t.rateLimiters[rlKey] = rl
	}

	return rateLimitedRoundTripper{
		RoundTripper: rt,
		token:        rl.token,
	}
}

type rateLimitedRoundTripper struct {
	http.RoundTripper
	token <-chan struct{}
}

func (rt rateLimitedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	<-rt.token
	return rt.RoundTripper.RoundTrip(req)
}

var rateLimiters = make(map[string]RateLimit)
