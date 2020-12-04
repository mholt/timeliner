package googlephotos

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mholt/archiver/v3"
	"github.com/mholt/timeliner"
)

func (c *Client) listFromTakeoutArchive(ctx context.Context, itemChan chan<- *timeliner.ItemGraph, opt timeliner.Options) error {
	err := archiver.Walk(opt.Filename, func(f archiver.File) error {
		pathInArchive := getPathInArchive(f) // TODO: maybe this should be a function in the archiver lib

		// only walk in album folders, and look for metadata files
		if !strings.HasPrefix(pathInArchive, "Takeout/Google Photos/") {
			return nil
		}
		if f.Name() != albumMetadataFilename {
			return nil
		}

		// album metadata file; begin processing next album
		var albumMeta albumArchiveMetadata
		err := json.NewDecoder(f).Decode(&albumMeta)
		if err != nil {
			return fmt.Errorf("decoding album metadata file %s: %v", pathInArchive, err)
		}
		collection := timeliner.Collection{
			OriginalID:  albumMeta.AlbumData.Date.Timestamp, // TODO: we don't have one... this will not merge nicely with API imports!!
			Name:        &albumMeta.AlbumData.Title,
			Description: &albumMeta.AlbumData.Description,
		}

		albumPathInArchive := strings.TrimSuffix(pathInArchive, albumMetadataFilename)

		// get all the album's items using a separate walk that is constrained to this album's folder
		err = archiver.Walk(opt.Filename, func(f archiver.File) error {
			pathInArchive := getPathInArchive(f)
			if !strings.HasPrefix(pathInArchive, albumPathInArchive) {
				return nil
			}
			if f.Name() == albumMetadataFilename {
				return nil
			}
			if filepath.Ext(f.Name()) != ".json" {
				return nil
			}

			var itemMeta mediaArchiveMetadata
			err := json.NewDecoder(f).Decode(&itemMeta)
			if err != nil {
				return fmt.Errorf("decoding item metadata file %s: %v", pathInArchive, err)
			}

			itemMeta.parsedPhotoTakenTime, err = itemMeta.timestamp()
			if err != nil {
				return fmt.Errorf("parsing timestamp from item %s: %v", pathInArchive, err)
			}
			itemMeta.pathInArchive = strings.TrimSuffix(pathInArchive, ".json")
			itemMeta.archiveFilename = opt.Filename

			collection.Items = append(collection.Items, timeliner.CollectionItem{
				Item:     itemMeta,
				Position: len(collection.Items),
			})

			return nil
		})
		if err != nil {
			return err
		}

		ig := timeliner.NewItemGraph(nil)
		ig.Collections = append(ig.Collections, collection)
		itemChan <- ig

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

const albumMetadataFilename = "metadata.json"

func getPathInArchive(f archiver.File) string {
	switch hdr := f.Header.(type) {
	case zip.FileHeader:
		return hdr.Name
	case tar.Header:
		return hdr.Name
	}
	return ""
}

type albumArchiveMetadata struct {
	AlbumData struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Access      string `json:"access"`
		Location    string `json:"location"`
		Date        struct {
			Timestamp string `json:"timestamp"`
			Formatted string `json:"formatted"`
		} `json:"date"`
		GeoData struct {
			Latitude      float64 `json:"latitude"`
			Longitude     float64 `json:"longitude"`
			Altitude      float64 `json:"altitude"`
			LatitudeSpan  float64 `json:"latitudeSpan"`
			LongitudeSpan float64 `json:"longitudeSpan"`
		} `json:"geoData"`
	} `json:"albumData"`
}

type mediaArchiveMetadata struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	ImageViews   string `json:"imageViews"`
	CreationTime struct {
		Timestamp string `json:"timestamp"`
		Formatted string `json:"formatted"`
	} `json:"creationTime"`
	ModificationTime struct {
		Timestamp string `json:"timestamp"`
		Formatted string `json:"formatted"`
	} `json:"modificationTime"`
	GeoData struct {
		Latitude      float64 `json:"latitude"`
		Longitude     float64 `json:"longitude"`
		Altitude      float64 `json:"altitude"`
		LatitudeSpan  float64 `json:"latitudeSpan"`
		LongitudeSpan float64 `json:"longitudeSpan"`
	} `json:"geoData"`
	GeoDataExif struct {
		Latitude      float64 `json:"latitude"`
		Longitude     float64 `json:"longitude"`
		Altitude      float64 `json:"altitude"`
		LatitudeSpan  float64 `json:"latitudeSpan"`
		LongitudeSpan float64 `json:"longitudeSpan"`
	} `json:"geoDataExif"`
	PhotoTakenTime struct {
		Timestamp string `json:"timestamp"`
		Formatted string `json:"formatted"`
	} `json:"photoTakenTime"`
	GooglePhotosOrigin struct {
		MobileUpload struct {
			DeviceFolder struct {
				LocalFolderName string `json:"localFolderName"`
			} `json:"deviceFolder"`
			DeviceType string `json:"deviceType"`
		} `json:"mobileUpload"`
	} `json:"googlePhotosOrigin"`

	parsedPhotoTakenTime time.Time
	archiveFilename      string
	pathInArchive        string
}

func (m mediaArchiveMetadata) timestamp() (time.Time, error) {
	ts := m.PhotoTakenTime.Timestamp
	if ts == "" {
		ts = m.CreationTime.Timestamp
	}
	if ts == "" {
		ts = m.ModificationTime.Timestamp
	}
	if ts == "" {
		return time.Time{}, fmt.Errorf("no timestamp available")
	}
	parsed, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(parsed, 0), nil
}

func (m mediaArchiveMetadata) ID() string {
	// TODO: THIS IS NOT THE SAME AS THE ID FROM THE API
	return m.PhotoTakenTime.Timestamp + "_" + m.Title
}

func (m mediaArchiveMetadata) Timestamp() time.Time {
	return m.parsedPhotoTakenTime
}

func (m mediaArchiveMetadata) Class() timeliner.ItemClass {
	ext := filepath.Ext(strings.ToLower(m.Title))
	switch ext {
	case ".mp4", ".m4v", ".mov", ".wmv", ".mkv", "mpeg4", ".mpeg", ".ogg", ".m4p", ".avi":
		return timeliner.ClassVideo
	default:
		return timeliner.ClassImage
	}
}

func (m mediaArchiveMetadata) Owner() (id *string, name *string) {
	return nil, nil
}

func (m mediaArchiveMetadata) DataText() (*string, error) {
	if m.Description != "" {
		return &m.Description, nil
	}
	return nil, nil
}

func (m mediaArchiveMetadata) DataFileName() *string {
	return &m.Title
}

func (m mediaArchiveMetadata) DataFileReader() (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := archiver.Walk(m.archiveFilename, func(f archiver.File) error {
		pathInArchive := getPathInArchive(f)
		if pathInArchive != m.pathInArchive {
			return nil
		}

		buf := new(bytes.Buffer)
		_, err := io.Copy(buf, f)
		if err != nil {
			return fmt.Errorf("copying item into memory: %v", err)
		}
		rc = timeliner.FakeCloser(buf)

		return archiver.ErrStopWalk
	})
	if err != nil {
		return nil, fmt.Errorf("walking takeout file %s in search of media: %v",
			m.archiveFilename, err)
	}
	return rc, nil
}

func (m mediaArchiveMetadata) DataFileHash() []byte {
	return nil
}

func (m mediaArchiveMetadata) DataFileMIMEType() *string {
	return nil
}

func (m mediaArchiveMetadata) Metadata() (*timeliner.Metadata, error) {
	return nil, nil
}

func (m mediaArchiveMetadata) Location() (*timeliner.Location, error) {
	lat, lon := m.GeoData.Latitude, m.GeoData.Longitude
	if lat == 0 {
		lat = m.GeoDataExif.Latitude
	}
	if lon == 0 {
		lon = m.GeoDataExif.Longitude
	}
	return &timeliner.Location{
		Latitude:  &lat,
		Longitude: &lon,
	}, nil
}
