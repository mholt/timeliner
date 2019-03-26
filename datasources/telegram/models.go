package telegram

import (
	"encoding/json"
	"github.com/mholt/timeliner"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type telegramArchive struct {
	Profile       telegramProfile       `json:"personal_information"`
	ChatContainer telegramChatContainer `json:"chats"`
}

type telegramChatContainer struct {
	Chats []telegramChat `json:"list"`
}

type telegramProfile struct {
	UserID      int    `json:"user_id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	PhoneNumber string `json:"phone_number				"`
	Username    string `json:"username"`
}

func (item telegramChat) ID() string {
	return strconv.Itoa(item.ChatID)
}

func (item telegramChat) Class() timeliner.ItemClass {
	return timeliner.CLassConversation
}

//  ------------------------------- Telegram Chat ---------------------------------------------------------

type telegramChat struct {
	ChatID   int               `json:"id"`
	Name     string            `json:"name"`
	ChatType string            `json:"type"`
	Messages []telegramMessage `json:"messages"`

	ownerID          string
	ownerName        string
	firstMessageTime time.Time
}

func (item telegramChat) Timestamp() time.Time {
	return item.firstMessageTime
}

func (item telegramChat) Owner() (id *string, name *string) {
	return &item.ownerID, &item.ownerName
}

func (item telegramChat) DataText() (*string, error) {
	return nil, nil
}

func (item telegramChat) DataFileName() *string {
	return nil
}

func (item telegramChat) DataFileReader() (io.ReadCloser, error) {
	return nil, nil
}

func (item telegramChat) DataFileHash() []byte {
	return nil
}

func (item telegramChat) DataFileMIMEType() *string {
	return nil
}

func (item telegramChat) Metadata() (*timeliner.Metadata, error) {
	return nil, nil
}

func (item telegramChat) Location() (*timeliner.Location, error) {
	return nil, nil
}

//  ------------------------------- Telegram Message ---------------------------------------------------------

type telegramMessage struct {
	MessageID           int                         `json:"id"`
	Date                string                      `json:"date"`
	Edited              string                      `json:"edited"`
	From_id             int                         `json:"from_id"`
	From                string                      `json:"from"`
	Text                telegramMessageText         `json:"text,omitempty"`
	MediaType           string                      `json:"media_type,omitempty"`
	FileRaw             string                      `json:"file,omitempty"`
	Thumbnail           string                      `json:"thumbnail,omitempty"`
	Width               int                         `json:"width,omitempty"`
	Height              int                         `json:"height,omitempty"`
	PhotoRaw            string                      `json:"photo,omitempty"`
	MimeType            string                      `json:"mime_type,omitempty"`
	ViaBot              string                      `json:"via_bot,omitempty"`
	DurationSeconds     int                         `json:"duration_seconds,omitempty"`
	LocationInformation telegramLocationInformation `json:"location_information,omitempty"`

	AbsFilePath    string
	FromIDParsed   string
	DateParsed     time.Time
	EditedParsed   time.Time
	ConversationID string
}

type telegramComplexMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type telegramLocationInformation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type telegramMessageText string

// the text attribute is sometimes just a string and sometimes an array of strings or objects, that themselves contain a text and type attribute
// this type either returns the string or concatenates all strings and object's text attributes into a string
func (raw *telegramMessageText) UnmarshalJSON(b []byte) error {
	// return the string, if it is a string
	if b[0] == '"' {
		return json.Unmarshal(b, (*string)(raw))
	}

	contents := []json.RawMessage{}

	err := json.Unmarshal(b, &contents)
	if err != nil {
		panic(err)
	}

	// loop through list and concatenate the unmarshalled strings
	var str strings.Builder

	for _, c := range contents {
		if c[0] == '"' {
			// parsing string object directly
			var s string
			err := json.Unmarshal(c, (*string)(&s))

			if err != nil {
				panic(err)
			}

			str.WriteString(s)

		} else {
			// parsing a json object's text attribute and ignoring the rest
			var m telegramComplexMessageContent
			err := json.Unmarshal(c, (*telegramComplexMessageContent)(&m))

			if err != nil {
				panic(err)
			}
			str.WriteString(m.Text)
		}
	}

	*raw = telegramMessageText(str.String())

	return nil
}

func (item telegramMessage) ID() string {
	return strconv.Itoa(item.MessageID)
}

func (item telegramMessage) Timestamp() time.Time {
	return item.DateParsed
}

func (item telegramMessage) Class() timeliner.ItemClass {
	return timeliner.ClassPrivateMessage
}

func (item telegramMessage) Owner() (id *string, name *string) {
	return &item.FromIDParsed, &item.From
}

func (item telegramMessage) DataText() (*string, error) {
	return (*string)(&item.Text), nil
}

func (item telegramMessage) DataFileName() *string {
	// Making filenames "more unique" here by prepending a hash based on the conversation id and the timestamp to the filename
	// This hopefully avoids naming collisions and overwritten files during the import
	// (e.g. redundant names like "giphy.mp4" or "sticker.webp" are actually different files with identical names...

	var fpathname, rawfname = filepath.Split(item.AbsFilePath)

	h := fnv.New32a()
	_, _ = h.Write([]byte(item.Date + item.ConversationID))
	uid := strconv.FormatUint(uint64(h.Sum32()), 10)

	item.AbsFilePath = filepath.Join(fpathname, uid+"-"+rawfname)

	fname := filepath.Base(item.AbsFilePath)
	return &fname
}

func (item telegramMessage) DataFileReader() (io.ReadCloser, error) {
	if item.AbsFilePath == "" {
		return nil, nil
	} else {
		f, err := os.Open(item.AbsFilePath)
		return f, err
	}
}

func (item telegramMessage) DataFileHash() []byte {
	return nil
}

func (item telegramMessage) DataFileMIMEType() *string {
	return &item.MimeType
}

func (item telegramMessage) Metadata() (*timeliner.Metadata, error) {
	return &timeliner.Metadata{
		EditedDate: item.EditedParsed,
		MediaType:  item.MediaType,
		Width:      item.Width,
		Height:     item.Height,
	}, nil
}

func (item telegramMessage) Location() (*timeliner.Location, error) {
	return &timeliner.Location{
		&item.LocationInformation.Latitude,
		&item.LocationInformation.Longitude,
	}, nil
}
