package twitter

import (
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mholt/timeliner"
)

type tweet struct {
	Retweeted            bool              `json:"retweeted"` // always false (at least, with the export file)
	Source               string            `json:"source"`
	Entities             *tweetEntities    `json:"entities,omitempty"` // DO NOT USE (https://developer.twitter.com/en/docs/tweets/data-dictionary/overview/entities-object.html#media)
	DisplayTextRange     []string          `json:"display_text_range"`
	FavoriteCount        string            `json:"favorite_count"`
	TweetIDStr           string            `json:"id_str"`
	Truncated            bool              `json:"truncated"`
	Geo                  *tweetGeo         `json:"geo,omitempty"` // deprecated, see coordinates
	Coordinates          *tweetGeo         `json:"coordinates,omitempty"`
	RetweetCount         string            `json:"retweet_count"`
	TweetID              string            `json:"id"`
	InReplyToStatusID    string            `json:"in_reply_to_status_id,omitempty"`
	InReplyToStatusIDStr string            `json:"in_reply_to_status_id_str,omitempty"`
	CreatedAt            string            `json:"created_at"`
	Favorited            bool              `json:"favorited"`
	FullText             string            `json:"full_text"`
	Lang                 string            `json:"lang"`
	InReplyToScreenName  string            `json:"in_reply_to_screen_name,omitempty"`
	InReplyToUserID      string            `json:"in_reply_to_user_id,omitempty"`
	InReplyToUserIDStr   string            `json:"in_reply_to_user_id_str,omitempty"`
	PossiblySensitive    bool              `json:"possibly_sensitive,omitempty"`
	ExtendedEntities     *extendedEntities `json:"extended_entities,omitempty"`
	WithheldCopyright    bool              `json:"withheld_copyright,omitempty"`
	WithheldInCountries  []string          `json:"withheld_in_countries,omitempty"`
	WithheldScope        string            `json:"withheld_scope,omitempty"`

	createdAtParsed time.Time
	ownerAccount    twitterAccount
}

func (t *tweet) ID() string {
	if t.TweetID != "" {
		return t.TweetID
	}
	return t.TweetIDStr
}

func (t *tweet) Timestamp() time.Time {
	return t.createdAtParsed
}

func (t *tweet) Class() timeliner.ItemClass {
	return timeliner.ClassPost
}

func (t *tweet) Owner() (id *string, name *string) {
	return &t.ownerAccount.AccountID, &t.ownerAccount.Username
}

func (t *tweet) DataText() (*string, error) {
	return &t.FullText, nil
}

func (t *tweet) DataFileName() *string {
	if t.hasExactlyOneMediaItem() {
		return t.ExtendedEntities.Media[0].DataFileName()
	}
	return nil
}

func (t *tweet) DataFileReader() (io.ReadCloser, error) {
	if t.hasExactlyOneMediaItem() {
		return t.ExtendedEntities.Media[0].DataFileReader()
	}
	return nil, nil
}

func (t *tweet) DataFileHash() []byte {
	if t.hasExactlyOneMediaItem() {
		return t.ExtendedEntities.Media[0].DataFileHash()
	}
	return nil
}

func (t *tweet) DataFileMIMEType() *string {
	if t.hasExactlyOneMediaItem() {
		return t.ExtendedEntities.Media[0].DataFileMIMEType()
	}
	return nil
}

func (t *tweet) Metadata() (*timeliner.Metadata, error) {
	return nil, nil // TODO
}

func (t *tweet) Location() (*timeliner.Location, error) {
	return nil, nil // TODO
}

func (t *tweet) isRetweet() bool {
	if t.Retweeted {
		return true
	}
	// TODO: For some reason, when exporting one's Twitter data,
	// it always sets "retweeted" to false, even when "full_text"
	// clearly shows it's a retweet by prefixing it with "RT @"
	// - this seems like a bug with Twitter's exporter
	return strings.HasPrefix(t.FullText, "RT @")
}

func (t *tweet) hasExactlyOneMediaItem() bool {
	// "All Tweets with attached photos, videos and animated GIFs will include an
	// extended_entities JSON object. The extended_entities object contains a single
	// media array of media objects (see the entities section for its data
	// dictionary). No other entity types, such as hashtags and links, are included
	// in the extended_entities section."
	// https://developer.twitter.com/en/docs/tweets/data-dictionary/overview/extended-entities-object.html
	return t.ExtendedEntities != nil && len(t.ExtendedEntities.Media) == 1
}

type tweetGeo struct {
	Type        string   `json:"type"`
	Coordinates []string `json:"coordinates"` // "latitude, then a longitude"
}

type tweetPlace struct {
	ID          string      `json:"id"`
	URL         string      `json:"url"`
	PlaceType   string      `json:"place_type"`
	Name        string      `json:"name"`
	FullName    string      `json:"full_name"`
	CountryCode string      `json:"country_code"`
	Country     string      `json:"country"`
	BoundingBox boundingBox `json:"bounding_box"`
}

type boundingBox struct {
	Type string `json:"type"`

	// "A series of longitude and latitude points, defining a box which will contain
	// the Place entity this bounding box is related to. Each point is an array in
	// the form of [longitude, latitude]. Points are grouped into an array per bounding
	// box. Bounding box arrays are wrapped in one additional array to be compatible
	// with the polygon notation."
	Coordinates [][][]float64 `json:"coordinates"`
}

type tweetEntities struct {
	Hashtags     []hashtagEntity     `json:"hashtags"`
	Symbols      []symbolEntity      `json:"symbols"`
	UserMentions []userMentionEntity `json:"user_mentions"`
	URLs         []urlEntity         `json:"urls"`
	Polls        []pollEntity        `json:"polls"`
}

type hashtagEntity struct {
	Indices []string `json:"indices"`
	Text    string   `json:"text"`
}

type symbolEntity struct {
	Indices []string `json:"indices"`
	Text    string   `json:"text"`
}

type urlEntity struct {
	URL         string            `json:"url"`
	ExpandedURL string            `json:"expanded_url"`
	DisplayURL  string            `json:"display_url"`
	Unwound     *urlEntityUnwound `json:"unwound,omitempty"`
	Indices     []string          `json:"indices"`
}

type urlEntityUnwound struct {
	URL         string `json:"url"`
	Status      int    `json:"status"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type userMentionEntity struct {
	Name       string   `json:"name"`
	ScreenName string   `json:"screen_name"`
	Indices    []string `json:"indices"`
	IDStr      string   `json:"id_str"`
	ID         string   `json:"id"`
}

type pollEntity struct {
	Options         []pollOption `json:"options"`
	EndDatetime     string       `json:"end_datetime"`
	DurationMinutes int          `json:"duration_minutes"`
}

type pollOption struct {
	Position int    `json:"position"`
	Text     string `json:"text"`
}

type extendedEntities struct {
	Media []*mediaItem `json:"media"`
}

type mediaItem struct {
	ExpandedURL         string               `json:"expanded_url"`
	SourceStatusID      string               `json:"source_status_id"`
	Indices             []string             `json:"indices"`
	URL                 string               `json:"url"`
	MediaURL            string               `json:"media_url"`
	MediaIDStr          string               `json:"id_str"`
	VideoInfo           *videoInfo           `json:"video_info,omitempty"`
	SourceUserID        string               `json:"source_user_id"`
	AdditionalMediaInfo *additionalMediaInfo `json:"additional_media_info,omitempty"`
	MediaID             string               `json:"id"`
	MediaURLHTTPS       string               `json:"media_url_https"`
	SourceUserIDStr     string               `json:"source_user_id_str"`
	Sizes               mediaSizes           `json:"sizes"`
	Type                string               `json:"type"`
	SourceStatusIDStr   string               `json:"source_status_id_str"`
	DisplayURL          string               `json:"display_url"`

	parent *tweet
	reader io.Reader
}

func (m *mediaItem) ID() string {
	if m.MediaID != "" {
		return m.MediaID
	}
	return m.MediaIDStr
}

func (m *mediaItem) Timestamp() time.Time {
	return m.parent.createdAtParsed
}

func (m *mediaItem) Class() timeliner.ItemClass {
	switch m.Type {
	case "photo":
		return timeliner.ClassImage
	case "animated_gif":
		fallthrough // Twitter encodes these as video files
	case "video":
		return timeliner.ClassVideo
	}
	return timeliner.ClassUnknown
}

func (m *mediaItem) Owner() (id *string, name *string) {
	return &m.SourceUserID, nil
}

func (m *mediaItem) DataText() (*string, error) {
	return nil, nil
}

func (m *mediaItem) DataFileName() *string {
	var source string
	switch m.Type {
	case "animated_gif":
		fallthrough
	case "video":
		_, _, source = m.getLargestVideo()
		u, err := url.Parse(source)
		if err == nil {
			source = path.Base(u.Path)
		} else {
			source = path.Base(source)
		}
	case "photo":
		// TODO -- how to get the largest, will there be multiple??
		mURL := m.getURL()
		u, err := url.Parse(mURL)
		if err == nil {
			source = path.Base(u.Path)
		} else {
			source = path.Base(mURL)
		}
	}
	filename := fmt.Sprintf("%s-%s", m.parent.TweetID, source)
	return &filename
}

func (m *mediaItem) DataFileReader() (io.ReadCloser, error) {
	if m.reader == nil {
		return nil, fmt.Errorf("missing data file reader; this is probably a bug: %+v - video info: %+v", m, m.VideoInfo)
	}
	return timeliner.FakeCloser(m.reader), nil
}

func (m *mediaItem) DataFileHash() []byte {
	return nil
}

func (m *mediaItem) DataFileMIMEType() *string {
	switch m.Type {
	case "animated_gif":
		fallthrough
	case "video":
		_, contentType, _ := m.getLargestVideo()
		return &contentType
	case "photo":
		fname := m.DataFileName()
		if fname == nil {
			return nil
		}
		ext := strings.ToLower(path.Ext(*fname))
		if len(ext) == 0 {
			return nil
		}
		suffix := ext[1:] // trim the leading dot
		if suffix == "jpg" {
			suffix = "jpeg"
		}
		mt := "image/" + suffix
		return &mt
	}
	return nil
}

func (m *mediaItem) Metadata() (*timeliner.Metadata, error) {
	return nil, nil // TODO
}

func (m *mediaItem) Location() (*timeliner.Location, error) {
	return nil, nil // TODO
}

// TODO: How to get the largest image file? (The importer only has a single copy available to it, but videos have multiple variants....)

func (m *mediaItem) getLargestVideo() (bitrate int, contentType, source string) {
	if m.VideoInfo == nil {
		return
	}
	bitrate = -1 // so that greater-than comparison below works for video bitrate=0 (animated_gif)
	for _, v := range m.VideoInfo.Variants {
		brInt, err := strconv.Atoi(v.Bitrate)
		if err != nil {
			continue
		}
		if brInt > bitrate {
			source = v.URL
			contentType = v.ContentType
			bitrate = brInt
		}
	}

	return
}

// TODO: This works only for images...
func (m *mediaItem) getURL() string {
	if m.MediaURLHTTPS != "" {
		return m.MediaURLHTTPS
	}
	return m.MediaURL
}

type additionalMediaInfo struct {
	Monetizable bool `json:"monetizable"`
}

type videoInfo struct {
	AspectRatio    []string        `json:"aspect_ratio"`
	DurationMillis string          `json:"duration_millis"`
	Variants       []videoVariants `json:"variants"`
}

type videoVariants struct {
	Bitrate     string `json:"bitrate,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	URL         string `json:"url"`
}

type mediaSizes struct {
	Thumb  mediaSize `json:"thumb"`
	Small  mediaSize `json:"small"`
	Medium mediaSize `json:"medium"`
	Large  mediaSize `json:"large"`
}

type mediaSize struct {
	W      string `json:"w"`
	H      string `json:"h"`
	Resize string `json:"resize"` // fit|crop
}

type twitterUser struct {
	ID                   int64       `json:"id"`
	IDStr                string      `json:"id_str"`
	Name                 string      `json:"name"`
	ScreenName           string      `json:"screen_name"`
	Location             string      `json:"location"`
	URL                  string      `json:"url"`
	Description          string      `json:"description"`
	Verified             bool        `json:"verified"`
	FollowersCount       int         `json:"followers_count"`
	FriendsCount         int         `json:"friends_count"`
	ListedCount          int         `json:"listed_count"`
	FavouritesCount      int         `json:"favourites_count"`
	StatusesCount        int         `json:"statuses_count"`
	CreatedAt            string      `json:"created_at"`
	UTCOffset            interface{} `json:"utc_offset"`
	TimeZone             interface{} `json:"time_zone"`
	GeoEnabled           bool        `json:"geo_enabled"`
	Lang                 string      `json:"lang"`
	ProfileImageURLHTTPS string      `json:"profile_image_url_https"`

	// TODO: more fields exist; need to get actual example to build struct from
}
type twitterAccountFile []struct {
	Account twitterAccount `json:"account"`
}

type twitterAccount struct {
	PhoneNumber        string    `json:"phoneNumber"`
	Email              string    `json:"email"`
	CreatedVia         string    `json:"createdVia"`
	Username           string    `json:"username"`
	AccountID          string    `json:"accountId"`
	CreatedAt          time.Time `json:"createdAt"`
	AccountDisplayName string    `json:"accountDisplayName"`
}
