// Package instagram implements a Timeliner data source for
// importing data from Instagram archive files.
package instagram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/mholt/archiver/v3"
	"github.com/mholt/timeliner"
)

// Data source name and ID
const (
	DataSourceName = "Instagram"
	DataSourceID   = "instagram"
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
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.ListingOptions) error {
	defer close(itemChan)

	if opt.Filename == "" {
		return fmt.Errorf("filename is required")
	}

	// first, load the profile information
	prof, err := c.getProfileInfo(opt.Filename)
	if err != nil {
		return fmt.Errorf("loading profile: %v", err)
	}

	// then, load the media index
	idx, err := c.getMediaIndex(opt.Filename)
	if err != nil {
		return fmt.Errorf("loading index: %v", err)
	}

	// prepare each media item with the information they
	// need to be processed into the timeline
	for i, ph := range idx.Photos {
		idx.Photos[i].profile = prof
		idx.Photos[i].archiveFilename = opt.Filename
		idx.Photos[i].takenAtParsed, err = time.Parse(takenAtFormat, ph.TakenAt)
		if err != nil {
			return fmt.Errorf("parsing photo time %s into format %s: %v", ph.TakenAt, takenAtFormat, err)
		}
	}
	for i, p := range idx.Profile {
		idx.Profile[i].profile = prof
		idx.Profile[i].archiveFilename = opt.Filename
		idx.Photos[i].takenAtParsed, err = time.Parse(takenAtFormat, p.TakenAt)
		if err != nil {
			return fmt.Errorf("parsing profile pic time %s into format %s: %v", p.TakenAt, takenAtFormat, err)
		}
	}
	for i, vid := range idx.Videos {
		idx.Videos[i].profile = prof
		idx.Videos[i].archiveFilename = opt.Filename
		idx.Videos[i].takenAtParsed, err = time.Parse(takenAtFormat, vid.TakenAt)
		if err != nil {
			return fmt.Errorf("parsing video time %s into format %s: %v", vid.TakenAt, takenAtFormat, err)
		}
	}

	// add all of the media items to the timeline
	for _, photo := range idx.Photos {
		itemChan <- timeliner.NewItemGraph(photo)
	}
	for _, video := range idx.Videos {
		itemChan <- timeliner.NewItemGraph(video)
	}

	return nil
}

func (c *Client) getProfileInfo(filename string) (instaAccountProfile, error) {
	var prof instaAccountProfile
	err := archiver.Walk(filename, func(f archiver.File) error {
		defer f.Close()
		if f.Name() != "profile.json" {
			return nil
		}

		err := json.NewDecoder(f).Decode(&prof)
		if err != nil {
			return fmt.Errorf("decoding account file: %v", err)
		}

		return archiver.ErrStopWalk
	})
	return prof, err
}

func (c *Client) getMediaIndex(filename string) (instaMediaIndex, error) {
	var idx instaMediaIndex
	err := archiver.Walk(filename, func(f archiver.File) error {
		defer f.Close()
		if f.Name() != "media.json" {
			return nil
		}

		err := json.NewDecoder(f).Decode(&idx)
		if err != nil {
			return fmt.Errorf("decoding media index JSON: %v", err)
		}

		return archiver.ErrStopWalk
	})
	if err != nil {
		return idx, fmt.Errorf("walking archive file %s: %v", filename, err)
	}
	return idx, nil
}

const takenAtFormat = "2006-01-02T15:04:05+07:00"
