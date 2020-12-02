package instagram

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"time"

	"github.com/mholt/archiver/v3"
	"github.com/mholt/timeliner"
)

type instaMediaIndex struct {
	Photos  []instaPhoto      `json:"photos"`
	Profile []instaProfilePic `json:"profile"`
	Videos  []instaVideo      `json:"videos"`
}

type instaPhoto struct {
	Caption     string `json:"caption"`
	TakenAt     string `json:"taken_at"`
	Path        string `json:"path"`
	LocationStr string `json:"location,omitempty"`

	takenAtParsed   time.Time
	archiveFilename string
	profile         instaAccountProfile
}

func (ph instaPhoto) ID() string {
	fname := path.Base(ph.Path)
	ext := path.Ext(fname)
	return strings.TrimSuffix(fname, ext)
}

func (ph instaPhoto) Timestamp() time.Time {
	return ph.takenAtParsed
}

func (ph instaPhoto) Class() timeliner.ItemClass {
	return timeliner.ClassImage
}

func (ph instaPhoto) Owner() (id *string, name *string) {
	return &ph.profile.Username, &ph.profile.Name
}

func (ph instaPhoto) DataText() (*string, error) {
	return &ph.Caption, nil
}

func (ph instaPhoto) DataFileName() *string {
	fname := path.Base(ph.Path)
	return &fname
}

func (ph instaPhoto) DataFileReader() (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := archiver.Walk(ph.archiveFilename, func(f archiver.File) error {
		if f.Header.(zip.FileHeader).Name != ph.Path {
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
		return nil, fmt.Errorf("walking archive file %s in search of media: %v",
			ph.archiveFilename, err)
	}
	return rc, nil
}

func (ph instaPhoto) DataFileHash() []byte {
	return nil
}

func (ph instaPhoto) DataFileMIMEType() *string {
	mt := mime.TypeByExtension(path.Ext(ph.Path))
	return &mt
}

func (ph instaPhoto) Metadata() (*timeliner.Metadata, error) {
	if ph.LocationStr != "" {
		return &timeliner.Metadata{GeneralArea: ph.LocationStr}, nil
	}
	return nil, nil
}

func (ph instaPhoto) Location() (*timeliner.Location, error) {
	return nil, nil
}

type instaProfilePic struct {
	Caption         string `json:"caption"`
	TakenAt         string `json:"taken_at"`
	IsActiveProfile bool   `json:"is_active_profile"`
	Path            string `json:"path"`

	takenAtParsed   time.Time
	archiveFilename string
	profile         instaAccountProfile
}

type instaVideo struct {
	Caption     string `json:"caption"`
	TakenAt     string `json:"taken_at"`
	Path        string `json:"path"`
	LocationStr string `json:"location,omitempty"`

	takenAtParsed   time.Time
	archiveFilename string
	profile         instaAccountProfile
}

func (vid instaVideo) ID() string {
	fname := path.Base(vid.Path)
	ext := path.Ext(fname)
	return strings.TrimSuffix(fname, ext)
}

func (vid instaVideo) Timestamp() time.Time {
	return vid.takenAtParsed
}

func (vid instaVideo) Class() timeliner.ItemClass {
	return timeliner.ClassVideo
}

func (vid instaVideo) Owner() (id *string, name *string) {
	return &vid.profile.Username, &vid.profile.Name
}

func (vid instaVideo) DataText() (*string, error) {
	return &vid.Caption, nil
}

func (vid instaVideo) DataFileName() *string {
	fname := path.Base(vid.Path)
	return &fname
}

func (vid instaVideo) DataFileReader() (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := archiver.Walk(vid.archiveFilename, func(f archiver.File) error {
		if f.Header.(zip.FileHeader).Name != vid.Path {
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
		return nil, fmt.Errorf("walking archive file %s in search of media: %v",
			vid.archiveFilename, err)
	}
	return rc, nil
}

func (vid instaVideo) DataFileHash() []byte {
	return nil
}

func (vid instaVideo) DataFileMIMEType() *string {
	mt := mime.TypeByExtension(path.Ext(vid.Path))
	return &mt
}

func (vid instaVideo) Metadata() (*timeliner.Metadata, error) {
	if vid.LocationStr != "" {
		return &timeliner.Metadata{GeneralArea: vid.LocationStr}, nil
	}
	return nil, nil
}

func (vid instaVideo) Location() (*timeliner.Location, error) {
	return nil, nil
}

type instaAccountProfile struct {
	Biography      string `json:"biography"`
	DateJoined     string `json:"date_joined"`
	Email          string `json:"email"`
	Website        string `json:"website"`
	Gender         string `json:"gender"`
	PrivateAccount bool   `json:"private_account"`
	Name           string `json:"name"`
	PhoneNumber    string `json:"phone_number"`
	ProfilePicURL  string `json:"profile_pic_url"`
	Username       string `json:"username"`
}
