package twitter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/mholt/timeliner"
)

type tweet struct {
	Contributors         interface{}       `json:"contributors"`
	Coordinates          *tweetGeo         `json:"coordinates,omitempty"`
	CreatedAt            string            `json:"created_at"`
	DisplayTextRange     []transInt        `json:"display_text_range"`
	Entities             *twitterEntities  `json:"entities,omitempty"` // DO NOT USE (https://developer.twitter.com/en/docs/tweets/data-dictionary/overview/entities-object.html#media)
	ExtendedEntities     *extendedEntities `json:"extended_entities,omitempty"`
	FavoriteCount        transInt          `json:"favorite_count"`
	Favorited            bool              `json:"favorited"`
	FullText             string            `json:"full_text"`     // tweet_mode=extended (https://developer.twitter.com/en/docs/tweets/tweet-updates)
	Geo                  *tweetGeo         `json:"geo,omitempty"` // deprecated, see coordinates
	InReplyToScreenName  string            `json:"in_reply_to_screen_name,omitempty"`
	InReplyToStatusID    transInt          `json:"in_reply_to_status_id,omitempty"`
	InReplyToStatusIDStr string            `json:"in_reply_to_status_id_str,omitempty"`
	InReplyToUserID      transInt          `json:"in_reply_to_user_id,omitempty"`
	InReplyToUserIDStr   string            `json:"in_reply_to_user_id_str,omitempty"`
	IsQuoteStatus        bool              `json:"is_quote_status"`
	Lang                 string            `json:"lang"`
	Place                interface{}       `json:"place"`
	PossiblySensitive    bool              `json:"possibly_sensitive,omitempty"`
	RetweetCount         transInt          `json:"retweet_count"`
	Retweeted            bool              `json:"retweeted"`        // always false for some reason
	RetweetedStatus      *tweet            `json:"retweeted_status"` // API: contains full_text of a retweet (otherwise is truncated)
	Source               string            `json:"source"`
	Text                 string            `json:"text"`      // As of Feb. 2019, Twitter API default; truncated at ~140 chars (see FullText)
	Truncated            bool              `json:"truncated"` // API: always false in tweet_mode=extended, even if full_text is truncated (retweets)
	TweetID              transInt          `json:"id"`
	TweetIDStr           string            `json:"id_str"`
	User                 *twitterUser      `json:"user"`
	WithheldCopyright    bool              `json:"withheld_copyright,omitempty"`
	WithheldInCountries  []string          `json:"withheld_in_countries,omitempty"`
	WithheldScope        string            `json:"withheld_scope,omitempty"`

	createdAtParsed time.Time
	ownerAccount    twitterAccount
	source          string // "api|archive"
}

func (t *tweet) ID() string {
	return t.TweetIDStr
}

func (t *tweet) Timestamp() time.Time {
	return t.createdAtParsed
}

func (t *tweet) Class() timeliner.ItemClass {
	return timeliner.ClassPost
}

func (t *tweet) Owner() (id *string, name *string) {
	idStr := t.ownerAccount.id()
	nameStr := t.ownerAccount.screenName()
	if idStr != "" {
		id = &idStr
	}
	if nameStr != "" {
		name = &nameStr
	}
	return
}

func (t *tweet) DataText() (*string, error) {
	if txt := t.text(); txt != "" {
		return &txt, nil
	}
	return nil, nil
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
	if t.Retweeted || t.RetweetedStatus != nil {
		return true
	}
	// TODO: For some reason, when exporting one's Twitter data,
	// it always sets "retweeted" to false, even when "full_text"
	// clearly shows it's a retweet by prefixing it with "RT @"
	// - this seems like a bug with Twitter's exporter... okay
	// actually the API does it too, that's dumb
	return strings.HasPrefix(t.text(), "RT @")
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

func (t *tweet) text() string {
	// sigh, retweets get truncated if they're tall,
	// so we have to get the full text from a subfield
	if t.RetweetedStatus != nil {
		return strings.TrimSpace(fmt.Sprintf("RT @%s %s",
			t.RetweetedStatus.User.ScreenName, t.RetweetedStatus.text()))
	}
	if t.FullText != "" {
		return t.FullText
	}
	return t.Text
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

type twitterEntities struct {
	Hashtags     []hashtagEntity     `json:"hashtags"`
	Symbols      []symbolEntity      `json:"symbols"`
	UserMentions []userMentionEntity `json:"user_mentions"`
	URLs         []urlEntity         `json:"urls"`
	Polls        []pollEntity        `json:"polls"`
}

type hashtagEntity struct {
	Indices []transInt `json:"indices"`
	Text    string     `json:"text"`
}

type symbolEntity struct {
	Indices []transInt `json:"indices"`
	Text    string     `json:"text"`
}

type urlEntity struct {
	URL         string            `json:"url"`
	ExpandedURL string            `json:"expanded_url"`
	DisplayURL  string            `json:"display_url"`
	Unwound     *urlEntityUnwound `json:"unwound,omitempty"`
	Indices     []transInt        `json:"indices"`
}

type urlEntityUnwound struct {
	URL         string `json:"url"`
	Status      int    `json:"status"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type userMentionEntity struct {
	Name       string     `json:"name"`
	ScreenName string     `json:"screen_name"`
	Indices    []transInt `json:"indices"`
	IDStr      string     `json:"id_str"`
	ID         transInt   `json:"id"`
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
	AdditionalMediaInfo *additionalMediaInfo `json:"additional_media_info,omitempty"`
	DisplayURL          string               `json:"display_url"`
	ExpandedURL         string               `json:"expanded_url"`
	Indices             []transInt           `json:"indices"`
	MediaID             transInt             `json:"id"`
	MediaIDStr          string               `json:"id_str"`
	MediaURL            string               `json:"media_url"`
	MediaURLHTTPS       string               `json:"media_url_https"`
	Sizes               mediaSizes           `json:"sizes"`
	SourceStatusID      transInt             `json:"source_status_id"`
	SourceStatusIDStr   string               `json:"source_status_id_str"`
	SourceUserID        transInt             `json:"source_user_id"`
	SourceUserIDStr     string               `json:"source_user_id_str"`
	Type                string               `json:"type"`
	URL                 string               `json:"url"`
	VideoInfo           *videoInfo           `json:"video_info,omitempty"`

	parent     *tweet
	readCloser io.ReadCloser // access to the media contents
}

func (m *mediaItem) ID() string {
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
	if m.SourceUserIDStr == "" {
		return m.parent.Owner()
	}
	return &m.SourceUserIDStr, nil
}

func (m *mediaItem) DataText() (*string, error) {
	return nil, nil
}

func (m *mediaItem) DataFileName() *string {
	source := m.getURL()
	u, err := url.Parse(source)
	if err == nil {
		source = path.Base(u.Path)
	} else {
		source = path.Base(source)
	}
	// media in the export archives are prefixed by the
	// tweet ID they were posted with and a hyphen
	if m.parent.source == "archive" {
		source = fmt.Sprintf("%s-%s", m.parent.TweetIDStr, source)
	}
	return &source
}

func (m *mediaItem) DataFileReader() (io.ReadCloser, error) {
	if m.readCloser == nil {
		return nil, fmt.Errorf("missing data file reader; this is probably a bug: %+v -- video info (if any): %+v", m, m.VideoInfo)
	}
	return m.readCloser, nil
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

func (m *mediaItem) getLargestVideo() (bitrate int, contentType, source string) {
	if m.VideoInfo == nil {
		return
	}
	bitrate = -1 // so that greater-than comparison below works for video bitrate=0 (animated_gif)
	for _, v := range m.VideoInfo.Variants {
		if int(v.Bitrate) > bitrate {
			source = v.URL
			contentType = v.ContentType
			bitrate = int(v.Bitrate)
		}
	}

	return
}

func (m *mediaItem) getURL() string {
	switch m.Type {
	case "animated_gif":
		fallthrough
	case "video":
		_, _, source := m.getLargestVideo()
		return source
	case "photo":
		// the size of the photo can be adjusted
		// when downloading by appending a size
		// to the end of the URL: ":thumb", ":small",
		// ":medium", ":large", or ":orig" -- but
		// we don't do that here, only do that when
		// actually downloading
		if m.MediaURLHTTPS != "" {
			return m.MediaURLHTTPS
		}
		return m.MediaURL
	}
	return ""
}

type additionalMediaInfo struct {
	Monetizable bool `json:"monetizable"`
}

type videoInfo struct {
	AspectRatio    []transFloat    `json:"aspect_ratio"`
	DurationMillis transInt        `json:"duration_millis"`
	Variants       []videoVariants `json:"variants"`
}

type videoVariants struct {
	Bitrate     transInt `json:"bitrate,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	URL         string   `json:"url"`
}

type mediaSizes struct {
	Thumb  mediaSize `json:"thumb"`
	Small  mediaSize `json:"small"`
	Medium mediaSize `json:"medium"`
	Large  mediaSize `json:"large"`
}

type mediaSize struct {
	W      transInt `json:"w"`
	H      transInt `json:"h"`
	Resize string   `json:"resize"` // fit|crop
}

type twitterUser struct {
	ContributorsEnabled            bool             `json:"contributors_enabled"`
	CreatedAt                      string           `json:"created_at"`
	DefaultProfile                 bool             `json:"default_profile"`
	DefaultProfileImage            bool             `json:"default_profile_image"`
	Description                    string           `json:"description"`
	Entities                       *twitterEntities `json:"entities"`
	FavouritesCount                int              `json:"favourites_count"`
	FollowersCount                 int              `json:"followers_count"`
	Following                      interface{}      `json:"following"`
	FollowRequestSent              interface{}      `json:"follow_request_sent"`
	FriendsCount                   int              `json:"friends_count"`
	GeoEnabled                     bool             `json:"geo_enabled"`
	HasExtendedProfile             bool             `json:"has_extended_profile"`
	IsTranslationEnabled           bool             `json:"is_translation_enabled"`
	IsTranslator                   bool             `json:"is_translator"`
	Lang                           string           `json:"lang"`
	ListedCount                    int              `json:"listed_count"`
	Location                       string           `json:"location"`
	Name                           string           `json:"name"`
	Notifications                  interface{}      `json:"notifications"`
	ProfileBackgroundColor         string           `json:"profile_background_color"`
	ProfileBackgroundImageURL      string           `json:"profile_background_image_url"`
	ProfileBackgroundImageURLHTTPS string           `json:"profile_background_image_url_https"`
	ProfileBackgroundTile          bool             `json:"profile_background_tile"`
	ProfileBannerURL               string           `json:"profile_banner_url"`
	ProfileImageURL                string           `json:"profile_image_url"`
	ProfileImageURLHTTPS           string           `json:"profile_image_url_https"`
	ProfileLinkColor               string           `json:"profile_link_color"`
	ProfileSidebarBorderColor      string           `json:"profile_sidebar_border_color"`
	ProfileSidebarFillColor        string           `json:"profile_sidebar_fill_color"`
	ProfileTextColor               string           `json:"profile_text_color"`
	ProfileUseBackgroundImage      bool             `json:"profile_use_background_image"`
	Protected                      bool             `json:"protected"`
	ScreenName                     string           `json:"screen_name"`
	StatusesCount                  int              `json:"statuses_count"`
	TimeZone                       interface{}      `json:"time_zone"`
	TranslatorType                 string           `json:"translator_type"`
	URL                            string           `json:"url"`
	UserID                         transInt         `json:"id"`
	UserIDStr                      string           `json:"id_str"`
	UtcOffset                      interface{}      `json:"utc_offset"`
	Verified                       bool             `json:"verified"`
}

type twitterAccountFile []struct {
	Account twitterAccount `json:"account"`
}

type twitterAccount struct {
	// fields from export archive file: account.js
	PhoneNumber        string `json:"phoneNumber"`
	Email              string `json:"email"`
	CreatedVia         string `json:"createdVia"`
	Username           string `json:"username"`
	AccountID          string `json:"accountId"`
	AccountDisplayName string `json:"accountDisplayName"`

	// fields from API endpoint: GET users/show
	ID                             int         `json:"id"`
	IDStr                          string      `json:"id_str"`
	Name                           string      `json:"name"`
	ScreenName                     string      `json:"screen_name"`
	Location                       string      `json:"location"`
	ProfileLocation                interface{} `json:"profile_location"`
	Description                    string      `json:"description"`
	URL                            string      `json:"url"`
	Protected                      bool        `json:"protected"`
	FollowersCount                 int         `json:"followers_count"`
	FriendsCount                   int         `json:"friends_count"`
	ListedCount                    int         `json:"listed_count"`
	FavouritesCount                int         `json:"favourites_count"`
	UtcOffset                      interface{} `json:"utc_offset"`
	TimeZone                       interface{} `json:"time_zone"`
	GeoEnabled                     bool        `json:"geo_enabled"`
	Verified                       bool        `json:"verified"`
	StatusesCount                  int         `json:"statuses_count"`
	Lang                           string      `json:"lang"`
	Status                         *tweet      `json:"status"`
	ContributorsEnabled            bool        `json:"contributors_enabled"`
	IsTranslator                   bool        `json:"is_translator"`
	IsTranslationEnabled           bool        `json:"is_translation_enabled"`
	ProfileBackgroundColor         string      `json:"profile_background_color"`
	ProfileBackgroundImageURL      string      `json:"profile_background_image_url"`
	ProfileBackgroundImageURLHTTPS string      `json:"profile_background_image_url_https"`
	ProfileBackgroundTile          bool        `json:"profile_background_tile"`
	ProfileImageURL                string      `json:"profile_image_url"`
	ProfileImageURLHTTPS           string      `json:"profile_image_url_https"`
	ProfileBannerURL               string      `json:"profile_banner_url"`
	ProfileLinkColor               string      `json:"profile_link_color"`
	ProfileSidebarBorderColor      string      `json:"profile_sidebar_border_color"`
	ProfileSidebarFillColor        string      `json:"profile_sidebar_fill_color"`
	ProfileTextColor               string      `json:"profile_text_color"`
	ProfileUseBackgroundImage      bool        `json:"profile_use_background_image"`
	HasExtendedProfile             bool        `json:"has_extended_profile"`
	DefaultProfile                 bool        `json:"default_profile"`
	DefaultProfileImage            bool        `json:"default_profile_image"`
	Following                      interface{} `json:"following"`
	FollowRequestSent              interface{} `json:"follow_request_sent"`
	Notifications                  interface{} `json:"notifications"`
	TranslatorType                 string      `json:"translator_type"`

	// fields in both export archive file and API
	CreatedAt string `json:"createdAt"` // NOTE: string with API, time.Time from archive
}

func (ta twitterAccount) screenName() string {
	if ta.ScreenName != "" {
		return ta.ScreenName
	}
	return ta.Username
}

func (ta twitterAccount) id() string {
	if ta.IDStr != "" {
		return ta.IDStr
	}
	return ta.AccountID
}

func (ta twitterAccount) name() string {
	if ta.Name != "" {
		return ta.Name
	}
	return ta.AccountDisplayName
}

// transInt is an integer that could be
// unmarshaled from a string, too. This
// is needed because the archive JSON
// from Twitter uses all string values,
// but the same fields are integers with
// the API.
type transInt int

func (ti *transInt) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("no value")
	}
	b = bytes.Trim(b, "\"")
	var i int
	err := json.Unmarshal(b, &i)
	if err != nil {
		return err
	}
	*ti = transInt(i)
	return nil
}

// transFloat is like transInt but for floats.
type transFloat float64

func (tf *transFloat) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("no value")
	}
	b = bytes.Trim(b, "\"")
	var f float64
	err := json.Unmarshal(b, &f)
	if err != nil {
		return err
	}
	*tf = transFloat(f)
	return nil
}
