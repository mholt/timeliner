// Package googlelocation implements a Timeliner data source for
// importing data from the Google Location History (aka Google
// Maps Timeline).
package googlelocation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mholt/timeliner"
)

// Data source name and ID
const (
	DataSourceName = "Google Location History"
	DataSourceID   = "google_location"
)

var dataSource = timeliner.DataSource{
	ID:   DataSourceID,
	Name: DataSourceName,
	NewClient: func(acc timeliner.Account) (timeliner.Client, error) {
		return new(Client), nil
	},
}

func init() {
	err := timeliner.RegisterDataSource(dataSource)
	if err != nil {
		log.Fatal(err)
	}
}

// Client implements the timeliner.Client interface.
type Client struct{}

// ListItems lists items from the data source. opt.Filename must be non-empty.
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	defer close(itemChan)

	if opt.Filename == "" {
		return fmt.Errorf("filename is required")
	}

	file, err := os.Open(opt.Filename)
	if err != nil {
		return fmt.Errorf("opening data file: %v", err)
	}
	defer file.Close()

	dec := json.NewDecoder(file)

	// read the following opening tokens:
	// 1. open brace '{'
	// 2. "locations" field name,
	// 3. the array value's opening bracket '['
	for i := 0; i < 3; i++ {
		_, err := dec.Token()
		if err != nil {
			return fmt.Errorf("decoding opening token: %v", err)
		}
	}

	var prev *location
	for dec.More() {
		select {
		case <-ctx.Done():
			return nil
		default:
			var err error
			prev, err = c.processLocation(dec, prev, itemChan)
			if err != nil {
				return fmt.Errorf("processing location item: %v", err)
			}
		}
	}

	return nil
}

func (c *Client) processLocation(dec *json.Decoder, prev *location,
	itemChan chan<- *timeliner.ItemGraph) (*location, error) {

	var l *location
	err := dec.Decode(&l)
	if err != nil {
		return nil, fmt.Errorf("decoding location element: %v", err)
	}

	// redundancy checks (lots of data points are very similar)
	if prev != nil {
		// if the timestamp of this location is the same
		// as the previous one, it seems useless to keep
		// both, so skip this one (also, we produce IDs
		// based on timestamp, which must be unique --
		// hence why we compare the unix timestamp values)
		if l.Timestamp().Unix() == prev.Timestamp().Unix() {
			return l, nil
		}

		// if this location is basically the same spot as the
		// previously-seen one, and if we're sure that the
		// timestamps are in order, skip it; mostly redundant
		if locationsSimilar(l, prev) && l.Timestamp().Before(prev.Timestamp()) {
			return l, nil
		}
	}

	// store this item, and possibly connect it to the
	// previous one if there's a movement activity
	ig := timeliner.NewItemGraph(l)
	if movement := l.primaryMovement(); movement != "" && prev != nil {
		// bidirectional edge, because you may want to know how you got somewhere,
		// and the timestamps should make it obvious which location is the "from"
		// or the "to", since you can't go backwards in time (that we know of...)
		ig.Add(prev, timeliner.Relation{
			Label:         strings.ToLower(movement),
			Bidirectional: true,
		})
	}
	itemChan <- ig

	return l, nil
}

func locationsSimilar(a, b *location) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return similar(a.LatitudeE7, b.LatitudeE7) &&
		similar(a.LongitudeE7, b.LongitudeE7)
}

func similar(a, b int) bool {
	const tolerance = 1000
	if a > b {
		return a-b < tolerance
	}
	return b-a < tolerance
}

type location struct {
	TimestampMs      string       `json:"timestampMs"`
	LatitudeE7       int          `json:"latitudeE7"`
	LongitudeE7      int          `json:"longitudeE7"`
	Accuracy         int          `json:"accuracy"`
	Altitude         int          `json:"altitude,omitempty"`
	VerticalAccuracy int          `json:"verticalAccuracy,omitempty"`
	Activity         []activities `json:"activity,omitempty"`
	Velocity         int          `json:"velocity,omitempty"`
	Heading          int          `json:"heading,omitempty"`
}

func (l location) primaryMovement() string {
	if len(l.Activity) == 0 {
		return ""
	}

	counts := make(map[string]int)
	confidences := make(map[string]int)
	for _, a := range l.Activity {
		for _, aa := range a.Activity {
			counts[aa.Type]++
			confidences[aa.Type] += aa.Confidence
		}
	}

	// turn confidence into average confidence,
	// (ensure all activities are represented),
	// and keep activities with high enough score
	var top []activity
	var hasOnFoot, hasWalking, hasRunning bool
	for _, a := range movementActivities {
		count := counts[a]
		if count == 0 {
			count = 1 // for the purposes of division
		}
		avg := confidences[a] / len(l.Activity)
		avgSeen := confidences[a] / count
		if avgSeen > 50 {
			switch a {
			case "ON_FOOT":
				hasOnFoot = true
			case "WALKING":
				hasWalking = true
			case "RUNNING":
				hasRunning = true
			}
			top = append(top, activity{Type: a, Confidence: avg})
		}
	}
	sort.Slice(top, func(i, j int) bool {
		return top[i].Confidence > top[j].Confidence
	})

	// consolidate ON_FOOT, WALKING, and RUNNING if more than one is present
	if hasOnFoot && (hasWalking || hasRunning) {
		for i := 0; i < len(top); i++ {
			if hasWalking && hasRunning &&
				(top[i].Type == "WALKING" || top[i].Type == "RUNNING") {
				// if both WALKING and RUNNING, prefer more general ON_FOOT
				top = append(top[:i], top[i+1:]...)
			} else if top[i].Type == "ON_FOOT" {
				// if only one of WALKING or RUNNING, prefer that over ON_FOOT
				top = append(top[:i], top[i+1:]...)
			}
		}
	}

	if len(top) > 0 {
		return top[0].Type
	}
	return ""
}

func (l location) hasActivity(act string) bool {
	for _, a := range l.Activity {
		for _, aa := range a.Activity {
			if aa.Type == act && aa.Confidence > 50 {
				return true
			}
		}
	}
	return false
}

type activities struct {
	TimestampMs string     `json:"timestampMs"`
	Activity    []activity `json:"activity"`
}

type activity struct {
	Type       string `json:"type"`
	Confidence int    `json:"confidence"`
}

// ID returns a string representation of the timestamp,
// since there is no actual ID provided by the service.
// It is assumed that one cannot be in two places at once.
func (l location) ID() string {
	ts := fmt.Sprintf("loc_%d", l.Timestamp().Unix())
	return ts
}

func (l location) Timestamp() time.Time {
	ts, err := strconv.Atoi(l.TimestampMs)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(int64(ts)/1000, 0)
}

func (l location) Owner() (*string, *string) {
	return nil, nil
}

func (l location) Class() timeliner.ItemClass {
	return timeliner.ClassLocation
}

func (l location) DataText() (*string, error) {
	return nil, nil
}

func (l location) DataFileName() *string {
	return nil
}

func (l location) DataFileReader() (io.ReadCloser, error) {
	return nil, nil
}

func (l location) DataFileHash() []byte {
	return nil
}

func (l location) DataFileMIMEType() *string {
	return nil
}

func (l location) Metadata() (*timeliner.Metadata, error) {
	var m timeliner.Metadata
	var hasMetadata bool

	if l.Velocity > 0 {
		m.Velocity = l.Velocity
		hasMetadata = true
	}
	if l.Heading > 0 {
		m.Heading = l.Heading
		hasMetadata = true
	}
	if l.Altitude > 0 {
		m.Altitude = l.Altitude
		m.AltitudeAccuracy = l.VerticalAccuracy
		hasMetadata = true
	}

	if hasMetadata {
		return &m, nil
	}
	return nil, nil
}

func (l location) Location() (*timeliner.Location, error) {
	lat := float64(l.LatitudeE7) / 1e7
	lon := float64(l.LongitudeE7) / 1e7
	return &timeliner.Location{
		Latitude:  &lat,
		Longitude: &lon,
	}, nil
}

// movementActivities is the list of activities we care about
// for drawing relationships between two locations. For example,
// we don't care about TILTING (sudden accelerometer adjustment,
// like phone set down or person standing up), UNKNOWN, or STILL
// (where there is no apparent movement detected).
//
// https://developers.google.com/android/reference/com/google/android/gms/location/DetectedActivity
var movementActivities = []string{
	"WALKING",
	"RUNNING",
	"IN_VEHICLE",
	"ON_FOOT",
	"ON_BICYCLE",
}
