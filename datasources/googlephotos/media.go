package googlephotos

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/mholt/timeliner"
)

// listMediaItems is the structure of the results
// of calling mediaItems in the Google Photos API.
type listMediaItems struct {
	MediaItems    []mediaItem `json:"mediaItems"`
	NextPageToken string      `json:"nextPageToken"`
}

type mediaItem struct {
	MediaID         string           `json:"id"`
	ProductURL      string           `json:"productUrl"`
	BaseURL         string           `json:"baseUrl"`
	Description     string           `json:"description"`
	MIMEType        string           `json:"mimeType"`
	MediaMetadata   mediaMetadata    `json:"mediaMetadata"`
	ContributorInfo mediaContributor `json:"mediaContributor"`
	Filename        string           `json:"filename"`
}

func (m mediaItem) ID() string {
	return m.MediaID
}

func (m mediaItem) Timestamp() time.Time {
	return m.MediaMetadata.CreationTime
}

func (m mediaItem) DataText() (*string, error) {
	return &m.Description, nil
}

func (m mediaItem) DataFileName() *string {
	return &m.Filename
}

func (m mediaItem) DataFileReader() (io.ReadCloser, error) {
	if m.MediaMetadata.Video != nil && m.MediaMetadata.Video.Status != "READY" {
		log.Printf("[INFO] Skipping video file because it is not ready (status=%s filename=%s)",
			m.MediaMetadata.Video.Status, m.Filename)
		return nil, nil
	}

	u := m.BaseURL

	// configure for the download of full file with almost-full exif data; see
	// https://developers.google.com/photos/library/guides/access-media-items#base-urls
	if m.MediaMetadata.Photo != nil {
		u += "=d"
	} else if m.MediaMetadata.Video != nil {
		u += "=dv"
	}

	const maxTries = 5
	var err error
	var resp *http.Response
	for i := 0; i < maxTries; i++ {
		resp, err = http.Get(u)
		if err != nil {
			err = fmt.Errorf("getting media contents: %v", err)
			log.Printf("[ERROR][%s] %v - retrying... (attempt %d/%d)", DataSourceID, err, i+1, maxTries)
			time.Sleep(1 * time.Second)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			bodyText, err2 := ioutil.ReadAll(io.LimitReader(resp.Body, 1024*256))
			resp.Body.Close()

			if err2 == nil {
				err = fmt.Errorf("HTTP %d: %s: >>> %s <<<", resp.StatusCode, resp.Status, bodyText)
			} else {
				err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
			}

			log.Printf("[ERROR][%s] Bad response: %v - retrying... (attempt %d/%d)",
				DataSourceID, err, i+1, maxTries)
			time.Sleep(1 * time.Second)
			continue
		}
		break
	}

	return resp.Body, err
}

func (m mediaItem) DataFileHash() []byte {
	return nil
}

func (m mediaItem) DataFileMIMEType() *string {
	return &m.MIMEType
}

func (m mediaItem) Owner() (*string, *string) {
	// since we only download media owned by the account,
	// we can leave ID nil and assume the display name
	// is the account owner's name
	if m.ContributorInfo.DisplayName != "" {
		return nil, &m.ContributorInfo.DisplayName
	}
	return nil, nil
}

func (m mediaItem) Class() timeliner.ItemClass {
	if m.MediaMetadata.Video != nil {
		return timeliner.ClassVideo
	}
	if m.MediaMetadata.Photo != nil {
		return timeliner.ClassImage
	}
	return timeliner.ClassUnknown
}

func (m mediaItem) Metadata() (*timeliner.Metadata, error) {
	// TODO: Parse exif metadata... maybe add most important/useful
	// EXIF fields to the metadata struct directly?

	widthInt, err := strconv.Atoi(m.MediaMetadata.Width)
	if err != nil {
		return nil, fmt.Errorf("parsing width as int: %v (width=%s)",
			err, m.MediaMetadata.Width)
	}
	heightInt, err := strconv.Atoi(m.MediaMetadata.Height)
	if err != nil {
		return nil, fmt.Errorf("parsing height as int: %v (height=%s)",
			err, m.MediaMetadata.Height)
	}

	meta := &timeliner.Metadata{
		Width:  widthInt,
		Height: heightInt,
	}

	if m.MediaMetadata.Photo != nil {
		meta.CameraMake = m.MediaMetadata.Photo.CameraMake
		meta.CameraModel = m.MediaMetadata.Photo.CameraModel
		meta.FocalLength = m.MediaMetadata.Photo.FocalLength
		meta.ApertureFNumber = m.MediaMetadata.Photo.ApertureFNumber
		meta.ISOEquivalent = m.MediaMetadata.Photo.ISOEquivalent
		if m.MediaMetadata.Photo.ExposureTime != "" {
			expDur, err := time.ParseDuration(m.MediaMetadata.Photo.ExposureTime)
			if err != nil {
				return nil, fmt.Errorf("parsing exposure time as duration: %v (exposure_time=%s)",
					err, m.MediaMetadata.Photo.ExposureTime)
			}
			meta.ExposureTime = expDur
		}
	} else if m.MediaMetadata.Video != nil {
		meta.CameraMake = m.MediaMetadata.Video.CameraMake
		meta.CameraModel = m.MediaMetadata.Video.CameraModel
		meta.FPS = m.MediaMetadata.Video.FPS
	}

	return meta, nil
}

func (m mediaItem) Location() (*timeliner.Location, error) {
	// See https://issuetracker.google.com/issues/80379228 😭
	return nil, nil
}

type mediaMetadata struct {
	CreationTime time.Time      `json:"creationTime"`
	Width        string         `json:"width"`
	Height       string         `json:"height"`
	Photo        *photoMetadata `json:"photo,omitempty"`
	Video        *videoMetadata `json:"video,omitempty"`
}

type photoMetadata struct {
	CameraMake      string  `json:"cameraMake"`
	CameraModel     string  `json:"cameraModel"`
	FocalLength     float64 `json:"focalLength"`
	ApertureFNumber float64 `json:"apertureFNumber"`
	ISOEquivalent   int     `json:"isoEquivalent"`
	ExposureTime    string  `json:"exposureTime"` // TODO: Parse duration out of this...?
}

type videoMetadata struct {
	CameraMake  string  `json:"cameraMake"`
	CameraModel string  `json:"cameraModel"`
	FPS         float64 `json:"fps"`
	Status      string  `json:"status"`
}

type mediaContributor struct {
	ProfilePictureBaseURL string `json:"profilePictureBaseUrl"`
	DisplayName           string `json:"displayName"`
}

type listMediaItemsRequest struct {
	Filters   *listMediaItemsFilter `json:"filters,omitempty"`
	AlbumID   string                `json:"albumId,omitempty"`
	PageSize  int                   `json:"pageSize,omitempty"`
	PageToken string                `json:"pageToken,omitempty"`
}

type listMediaItemsFilter struct {
	DateFilter               listMediaItemsDateFilter      `json:"dateFilter"`
	IncludeArchivedMedia     bool                          `json:"includeArchivedMedia"`
	ExcludeNonAppCreatedData bool                          `json:"excludeNonAppCreatedData"`
	ContentFilter            listMediaItemsContentFilter   `json:"contentFilter"`
	MediaTypeFilter          listMediaItemsMediaTypeFilter `json:"mediaTypeFilter"`
}

type listMediaItemsDateFilter struct {
	Ranges []listMediaItemsFilterRange `json:"ranges,omitempty"`
	Dates  []filterDate                `json:"dates,omitempty"`
}

type listMediaItemsFilterRange struct {
	StartDate filterDate `json:"startDate"`
	EndDate   filterDate `json:"endDate"`
}

type filterDate struct {
	Month int `json:"month"`
	Day   int `json:"day"`
	Year  int `json:"year"`
}

type listMediaItemsContentFilter struct {
	ExcludedContentCategories []string `json:"excludedContentCategories,omitempty"`
	IncludedContentCategories []string `json:"includedContentCategories,omitempty"`
}

type listMediaItemsMediaTypeFilter struct {
	MediaTypes []string `json:"mediaTypes,omitempty"`
}

type listAlbums struct {
	Albums        []gpAlbum `json:"albums"`
	NextPageToken string    `json:"nextPageToken"`
}

type gpAlbum struct {
	ID                    string `json:"id"`
	Title                 string `json:"title,omitempty"`
	ProductURL            string `json:"productUrl"`
	MediaItemsCount       string `json:"mediaItemsCount"`
	CoverPhotoBaseURL     string `json:"coverPhotoBaseUrl"`
	CoverPhotoMediaItemID string `json:"coverPhotoMediaItemId"`
}
