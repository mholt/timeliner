// Package telegram implements a Timeliner data source for the Telegram messenger. (TODO)
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mholt/timeliner"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	DataSourceName = "Telegram" // TODO: brand name
	DataSourceID   = "telegram" // TODO: snake_cased unique name
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

const tgTimeFormat = "2006-01-02T15:04:05"

func tgTimeToGoTime(tgTime string, location *time.Location) time.Time {
	if tgTime == "" {
		return time.Time{}
	}
	ts, err := time.ParseInLocation(tgTimeFormat, tgTime, location)
	if err != nil {
		log.Printf("[ERROR] Parsing timestamp from Telegram: '%s' is not in '%s' format",
			tgTime, tgTimeFormat)
	}
	return ts
}

// Client implements the timeliner.Client interface.
type Client struct{}

// ListItems lists items from the data source.
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	defer close(itemChan)

	if opt.Filename == "" {
		return fmt.Errorf("filename is required")
	}

	//TODO: make the default timezone location a command line argument
	loc, _ := time.LoadLocation("Europe/Berlin")
	//if opt.Timezone == "" {
	//	return fmt.Errorf("timezone is required")
	//}

	//loc, err := time.LoadLocation(opt.Timezone)
	//if err != nil {
	//	return fmt.Errorf("invalid timezone argument: '%v'", err)
	//}

	file, err := os.Open(opt.Filename)
	if err != nil {
		return fmt.Errorf("opening data file: %v", err)
	}

	datadir := filepath.Dir(opt.Filename)

	defer file.Close()

	dec := json.NewDecoder(file)

	var prev *telegramArchive
	for dec.More() {
		select {
		case <-ctx.Done():
			return nil
		default:
			var err error
			prev, err = c.processTelegramArchive(dec, prev, itemChan)
			if err != nil {
				return fmt.Errorf("processing telegramArchive item: %v", err)
			}

			var collectionDescription = "Telegram Chat"

			for idc, _ := range prev.ChatContainer.Chats {
				chat := &prev.ChatContainer.Chats[idc];
				if len(chat.Messages) == 0 {
					continue
				}

				chat.ownerID = strconv.Itoa(prev.Profile.UserID)
				//TODO: Telegram offers optional attributes for First Name, Last Name and a Username. Decide/Concatenate!
				chat.ownerName = prev.Profile.FirstName + prev.Profile.LastName + "(" + prev.Profile.Username + ")"
				chat.firstMessageTime = tgTimeToGoTime(chat.Messages[0].Date, loc)

				var ig = timeliner.NewItemGraph(chat)

				col := timeliner.Collection{
					OriginalID:  chat.ID(),
					Name:        &chat.Name,
					Description: &collectionDescription,
				}

				for midx, message := range chat.Messages {
					message.FromIDParsed = strconv.Itoa(message.From_id)
					message.DateParsed = tgTimeToGoTime(message.Date, loc)
					message.EditedParsed = tgTimeToGoTime(message.Edited, loc)
					message.ConversationID = chat.ID()

					if message.FileRaw != "" {
						message.AbsFilePath = filepath.Join(datadir, message.FileRaw)
					} else if message.PhotoRaw != "" {
						message.AbsFilePath = filepath.Join(datadir, message.PhotoRaw)
					}

					col.Items = append(col.Items, timeliner.CollectionItem{
						Position: midx,
						Item:     message,
					})
				}

				ig.Collections = append(ig.Collections, col)
				itemChan <- ig
			}
		}
	}

	return nil
}

func (c *Client) processTelegramArchive(dec *json.Decoder, prev *telegramArchive,
	itemChan chan<- *timeliner.ItemGraph) (*telegramArchive, error) {

	var l *telegramArchive
	err := dec.Decode(&l)
	if err != nil {
		return nil, fmt.Errorf("decoding telegramArchive: %v", err)
	}
	return l, nil
}
