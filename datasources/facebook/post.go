package facebook

import (
	"io"
	"log"
	"time"

	"github.com/mholt/timeliner"
)

type fbPost struct {
	Attachments   fbPostAttachments `json:"attachments,omitempty"`
	BackdatedTime string            `json:"backdated_time,omitempty"`
	CreatedTime   string            `json:"created_time,omitempty"` // example format: "2018-12-22T19:10:30+0000"
	From          fbFrom            `json:"from,omitempty"`
	Link          string            `json:"link,omitempty"`
	Description   string            `json:"description,omitempty"`
	Message       string            `json:"message,omitempty"`
	Name          string            `json:"name,omitempty"`
	ParentID      string            `json:"parent_id,omitempty"`
	Place         *fbPlace          `json:"place,omitempty"`
	StatusType    string            `json:"status_type,omitempty"`
	Type          string            `json:"type,omitempty"`
	PostID        string            `json:"id,omitempty"`
}

func (p fbPost) ID() string {
	return p.PostID
}

func (p fbPost) Timestamp() time.Time {
	if p.BackdatedTime != "" {
		return fbTimeToGoTime(p.BackdatedTime)
	}
	return fbTimeToGoTime(p.CreatedTime)
}

func (p fbPost) DataText() (*string, error) {
	return &p.Message, nil
}

func (p fbPost) DataFileName() *string {
	return nil
}

func (p fbPost) DataFileReader() (io.ReadCloser, error) {
	return nil, nil
}

func (p fbPost) DataFileHash() []byte {
	return nil
}

func (p fbPost) DataFileMIMEType() *string {
	return nil
}

func (p fbPost) Owner() (*string, *string) {
	return &p.From.ID, &p.From.Name
}

func (p fbPost) Class() timeliner.ItemClass {
	return timeliner.ClassPost
}

func (p fbPost) Metadata() (*timeliner.Metadata, error) {
	return &timeliner.Metadata{
		Link:        p.Link,
		Description: p.Description,
		Name:        p.Name,
		ParentID:    p.ParentID,
		StatusType:  p.StatusType,
		Type:        p.Type,
	}, nil
}

func (p fbPost) Location() (*timeliner.Location, error) {
	if p.Place != nil {
		return &timeliner.Location{
			Latitude:  &p.Place.Location.Latitude,
			Longitude: &p.Place.Location.Longitude,
		}, nil
	}
	return nil, nil
}

type fbPostAttachments struct {
	Data []fbPostAttachmentData `json:"data"`
}

type fbPostAttachmentData struct {
	Media          fbPostAttachmentMedia  `json:"media,omitempty"`
	Target         fbPostAttachmentTarget `json:"target,omitempty"`
	Subattachments fbPostAttachments      `json:"subattachments,omitempty"`
	Title          string                 `json:"title,omitempty"`
	Type           string                 `json:"type,omitempty"`
	URL            string                 `json:"url,omitempty"`
}

type fbPostAttachmentMedia struct {
	Image fbPostAttachmentImage `json:"image,omitempty"`
}

type fbPostAttachmentImage struct {
	Height int    `json:"height,omitempty"`
	Src    string `json:"src,omitempty"`
	Width  int    `json:"width,omitempty"`
}

type fbPostAttachmentTarget struct {
	ID  string `json:"id,omitempty"`
	URL string `json:"url,omitempty"`
}

func fbTimeToGoTime(fbTime string) time.Time {
	if fbTime == "" {
		return time.Time{}
	}
	ts, err := time.Parse(fbTimeFormat, fbTime)
	if err != nil {
		log.Printf("[ERROR] Parsing timestamp from Facebook: '%s' is not in '%s' format",
			fbTime, fbTimeFormat)
	}
	return ts
}

const fbTimeFormat = "2006-01-02T15:04:05+0000"
