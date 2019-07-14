// Package smsbackuprestore implements a Timeliner data source for
// the Android SMS Backup & Restore app by SyncTech:
// https://synctech.com.au/sms-backup-restore/
package smsbackuprestore

import (
	"context"
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"log"
	"os"

	"github.com/mholt/timeliner"
	"github.com/ttacon/libphonenumber"
)

// Data source name and ID.
const (
	DataSourceName = "SMS Backup & Restore"
	DataSourceID   = "smsbackuprestore"
)

var dataSource = timeliner.DataSource{
	ID:   DataSourceID,
	Name: DataSourceName,
	NewClient: func(acc timeliner.Account) (timeliner.Client, error) {
		return &Client{account: acc}, nil
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
	// DefaultRegion is the region to assume for phone
	// numbers that do not have an explicit country
	// calling code. This value should be the ISO
	// 3166-1 alpha-2 standard region code.
	DefaultRegion string

	account timeliner.Account
}

// ListItems lists items from the data source.
func (c *Client) ListItems(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	defer close(itemChan)

	if opt.Filename == "" {
		return fmt.Errorf("filename is required")
	}

	// ensure the client's phone number is standardized
	// TODO: It would be better to have a hook in the account creation process to be able to do this
	ownerPhoneNum, err := c.standardizePhoneNumber(c.account.UserID)
	if err != nil {
		return fmt.Errorf("standardizing client phone number '%s': %v", c.account.UserID, err)
	}
	c.account.UserID = ownerPhoneNum

	xmlFile, err := os.Open(opt.Filename)
	if err != nil {
		return err
	}
	defer xmlFile.Close()

	var data Smses
	dec := xml.NewDecoder(xmlFile)
	err = dec.Decode(&data)
	if err != nil {
		return fmt.Errorf("decoding XML file: %v", err)
	}

	for i := range data.SMS {
		data.SMS[i].client = c
		itemChan <- timeliner.NewItemGraph(data.SMS[i])
	}

	for i := range data.MMS {
		data.MMS[i].client = c
		ig := timeliner.NewItemGraph(data.MMS[i])
		// TODO: establish relationships or create collections
		// for group texts (multiple recipients)
		itemChan <- ig
	}

	return nil
}

// fastHash hashes input using a fast 32-bit hashing algorithm
// and returns the hash as a hex-encoded string. Do not use this
// for cryptographic purposes. If the hashing fails for some
// reason, an empty string is returned.
func fastHash(input string) string {
	h := fnv.New32a()
	h.Write([]byte(input))
	return fmt.Sprintf("%x", h.Sum32())
}

// standardizePhoneNumber attempts to parse number and returns
// a standardized version in E164 format. If the number does
// not have an explicit region/country code, the country code
// for c.DefaultRegion is used instead.
//
// We chose E164 because that's what Twilio uses.
func (c *Client) standardizePhoneNumber(number string) (string, error) {
	ph, err := libphonenumber.Parse(number, c.DefaultRegion)
	if err != nil {
		return "", err
	}
	return libphonenumber.Format(ph, libphonenumber.E164), nil
}
