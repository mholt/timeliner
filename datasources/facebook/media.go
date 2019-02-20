package facebook

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/mholt/timeliner"
)

type fbMediaPage struct {
	Data   []fbMedia `json:"data"`
	Paging fbPaging  `json:"paging"`
}

// fbMedia is used for videos, photos, and albums.
type fbMedia struct {
	Album         fbAlbum       `json:"album,omitempty"`
	BackdatedTime string        `json:"backdated_time,omitempty"`
	CreatedTime   string        `json:"created_time,omitempty"`
	From          fbFrom        `json:"from,omitempty"`
	Images        []fbImage     `json:"images,omitempty"`
	UpdatedTime   string        `json:"updated_time,omitempty"`
	Description   string        `json:"description,omitempty"`
	Length        float64       `json:"length,omitempty"` // in seconds
	Message       string        `json:"message,omitempty"`
	Name          string        `json:"name,omitempty"`
	Place         *fbPlace      `json:"place,omitempty"`
	Photos        *fbMediaPage  `json:"photos,omitempty"`
	Source        string        `json:"source,omitempty"`
	Status        fbVideoStatus `json:"status,omitempty"`
	MediaID       string        `json:"id,omitempty"`

	// these fields added by us and used internally
	mediaType          string
	bestSourceURL      string
	bestSourceFilename string
	exifData           map[string]interface{}
}

func (m *fbMedia) fillFields(mediaType string) {
	m.mediaType = mediaType

	// get URL to actual media content; we'll need
	// it later, and by doing this now, we only have
	// to do it once
	switch mediaType {
	case "photo":
		_, _, m.bestSourceURL = m.getLargestImage()
	case "video":
		m.bestSourceURL = m.Source
	}
	if m.bestSourceURL != "" {
		sourceURL, err := url.Parse(m.bestSourceURL)
		if err != nil {
			// TODO: What to return in this case? return the error?
			log.Printf("[ERROR] Parsing media source URL to get filename: %v", err)
		}
		m.bestSourceFilename = path.Base(sourceURL.Path)
	}
}

func (m *fbMedia) ID() string {
	return m.MediaID
}

func (m *fbMedia) Timestamp() time.Time {
	if m.BackdatedTime != "" {
		return fbTimeToGoTime(m.BackdatedTime)
	}
	return fbTimeToGoTime(m.CreatedTime)
}

func (m *fbMedia) DataText() (*string, error) {
	if m.Description != "" {
		return &m.Description, nil
	}
	if m.Name != "" {
		return &m.Name, nil
	}
	return nil, nil
}

func (m *fbMedia) DataFileName() *string {
	if m.bestSourceFilename != "" {
		return &m.bestSourceFilename
	}
	return nil
}

func (m *fbMedia) DataFileHash() []byte {
	return nil
}

func (m *fbMedia) DataFileReader() (io.ReadCloser, error) {
	if m.bestSourceURL == "" {
		return nil, fmt.Errorf("no way to get data file: no best source URL")
	}

	resp, err := http.Get(m.bestSourceURL)
	if err != nil {
		return nil, fmt.Errorf("getting media contents: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	return resp.Body, nil
}

func (m *fbMedia) DataFileMIMEType() *string {
	mt := mime.TypeByExtension(path.Ext(m.bestSourceFilename))
	if mt != "" {
		return &mt
	}
	return nil
}

func (m *fbMedia) Owner() (*string, *string) {
	return &m.From.ID, &m.From.Name
}

func (m *fbMedia) Class() timeliner.ItemClass {
	switch m.mediaType {
	case "photo":
		return timeliner.ClassImage
	case "video":
		return timeliner.ClassVideo
	}
	return timeliner.ClassUnknown
}

func (m *fbMedia) Metadata() (*timeliner.Metadata, error) {
	// TODO
	return nil, nil
}

func (m *fbMedia) getLargestImage() (height, width int, source string) {
	var largest int
	for _, im := range m.Images {
		size := im.Height * im.Width
		if size > largest {
			source = im.Source
			height = im.Height
			width = im.Width
			largest = size
		}
	}
	return
}

func (m *fbMedia) Location() (*timeliner.Location, error) {
	if m.Place != nil {
		return &timeliner.Location{
			Latitude:  &m.Place.Location.Latitude,
			Longitude: &m.Place.Location.Longitude,
		}, nil
	}
	return nil, nil
}

type fbVideoStatus struct {
	VideoStatus string `json:"video_status,omitempty"`
}

type fbAlbum struct {
	CreatedTime string        `json:"created_time,omitempty"`
	Name        string        `json:"name,omitempty"`
	ID          string        `json:"id,omitempty"`
	Photos      []fbMediaPage `json:"photos,omitempty"`
}

type fbImage struct {
	Height int    `json:"height,omitempty"`
	Source string `json:"source,omitempty"`
	Width  int    `json:"width,omitempty"`
}
