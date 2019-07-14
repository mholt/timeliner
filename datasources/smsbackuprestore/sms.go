package smsbackuprestore

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mholt/timeliner"
)

// Smses was generated 2019-07-10 using an export from
// SMS Backup & Restore v10.05.602 (previous versions
// have a bug with emoji encodings).
type Smses struct {
	XMLName    xml.Name `xml:"smses"`
	Text       string   `xml:",chardata"`
	Count      int      `xml:"count,attr"`
	BackupSet  string   `xml:"backup_set,attr"`  // UUID
	BackupDate int64    `xml:"backup_date,attr"` // unix timestamp in milliseconds
	SMS        []SMS    `xml:"sms"`
	MMS        []MMS    `xml:"mms"`
}

// CommonSMSandMMSFields are the fields that both
// SMS and MMS share in common.
type CommonSMSandMMSFields struct {
	Text         string `xml:",chardata"`
	Address      string `xml:"address,attr"`
	Date         int64  `xml:"date,attr"` // unix timestamp in milliseconds
	Read         int    `xml:"read,attr"`
	Locked       int    `xml:"locked,attr"`
	DateSent     int64  `xml:"date_sent,attr"` // unix timestamp in (SMS: milliseconds, MMS: seconds)
	SubID        int    `xml:"sub_id,attr"`
	ReadableDate string `xml:"readable_date,attr"` // format: "Oct 20, 2017 12:35:30 PM"
	ContactName  string `xml:"contact_name,attr"`  // might be "(Unknown)"
}

// SMS represents a simple text message.
type SMS struct {
	CommonSMSandMMSFields
	Protocol      int    `xml:"protocol,attr"`
	Type          int    `xml:"type,attr"` // 1 = received, 2 = sent
	Subject       string `xml:"subject,attr"`
	Body          string `xml:"body,attr"`
	Toa           string `xml:"toa,attr"`
	ScToa         string `xml:"sc_toa,attr"`
	ServiceCenter string `xml:"service_center,attr"`
	Status        int    `xml:"status,attr"`

	client *Client
}

// ID returns a unique ID for this text message.
// Because text messages do not have IDs, an ID
// is constructed by concatenating the millisecond
// timestamp of the message with a fast hash of
// the message body.
func (s SMS) ID() string {
	return fmt.Sprintf("%d_%s", s.Date, fastHash(s.Body))
}

// Timestamp returns the message's date.
func (s SMS) Timestamp() time.Time {
	return time.Unix(0, s.Date*int64(time.Millisecond))
}

// Class returns class Message.
func (s SMS) Class() timeliner.ItemClass {
	return timeliner.ClassMessage
}

// Owner returns the sender's phone number and name, if available.
func (s SMS) Owner() (number *string, name *string) {
	switch s.Type {
	case smsTypeSent:
		return &s.client.account.UserID, nil
	case smsTypeReceived:
		if s.ContactName != "" && s.ContactName != "(Unknown)" {
			name = &s.ContactName
		}
		standardized, err := s.client.standardizePhoneNumber(s.Address)
		if err == nil {
			number = &standardized
		} else {
			number = &s.Address // oh well
		}
	}
	return
}

// DataText returns the text of the message.
func (s SMS) DataText() (*string, error) {
	body := strings.TrimSpace(s.Body)
	if body != "" {
		return &body, nil
	}
	return nil, nil
}

// DataFileName returns nil.
func (s SMS) DataFileName() *string {
	return nil
}

// DataFileReader returns nil.
func (s SMS) DataFileReader() (io.ReadCloser, error) {
	return nil, nil
}

// DataFileHash returns nil.
func (s SMS) DataFileHash() []byte {
	return nil
}

// DataFileMIMEType returns nil.
func (s SMS) DataFileMIMEType() *string {
	return nil
}

// Metadata returns nil.
func (s SMS) Metadata() (*timeliner.Metadata, error) {
	return nil, nil
}

// Location returns nil.
func (s SMS) Location() (*timeliner.Location, error) {
	return nil, nil
}
