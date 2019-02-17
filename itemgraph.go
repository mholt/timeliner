package timeliner

import (
	"bytes"
	"encoding/gob"
	"io"
	"time"
)

// Item is the central concept of a piece of content
// from a service or data source. Take note of which
// methods are required to return non-empty values.
//
// The actual content of an item is stored either in
// the database or on disk as a file. Generally,
// content that is text-encoded can and should be
// stored in the database where it will be indexed.
// However, if the item's content (for example, the
// bytes of a photo or video) are not text or if the
// text is too large to store well in a database (for
// example, an entire novel), it should be stored
// on disk, and this interface has methods to
// accommodate both. Note that an item may have both
// text and non-text content, too: for example, photos
// and videos may have descriptions that are as much
// "content" as the media iteself. One part of an item
// is not mutually exclusive with any other.
type Item interface {
	// The unique ID of the item assigned by the service.
	// If the service does not assign one, then invent
	// one such that the ID is unique to the content or
	// substance of the item (for example, an ID derived
	// from timestamp or from the actual content of the
	// item -- whatever makes it unique). The ID need
	// only be unique for the account it is associated
	// with, although more unique is, of course, acceptable.
	//
	// REQUIRED.
	ID() string

	// The originating timestamp of the item, which
	// may be different from when the item was posted
	// or created. For example, a photo may be taken
	// one day but uploaded a week later. Prefer the
	// time when the original item content was captured.
	//
	// REQUIRED.
	Timestamp() time.Time

	// A classification of the item's kind.
	//
	// REQUIRED.
	Class() ItemClass

	// The user/account ID of the owner or
	// originator of the content, along with their
	// username or real name. The ID is used to
	// relate the item with the person behind it;
	// the name is used to make the person
	// recognizable to the human reader. If the
	// ID is nil, the current account owner will
	// be assumed. (Use the ID as given by the
	// data source.) If the data source only
	// provides a name but no ID, you may return
	// the name as the ID with the understanding
	// that a different name will be counted as a
	// different person. You may also return the
	// name as the name and leave the ID nil and
	// have correct results if it is safe to assume
	// the name belongs to the current account owner.
	Owner() (id *string, name *string)

	// Returns the text of the item, if any.
	// This field is indexed in the DB, so don't
	// use for unimportant metadata or huge
	// swaths of text; if there is a large
	// amount of text, use an item file instead.
	DataText() (*string, error)

	// For primary content which is not text or
	// which is too large to be stored well in a
	// database, the content can be downloaded
	// into a file. If so, the following methods
	// should return the necessary information,
	// if available from the service, so that a
	// data file can be obtained, stored, and
	// later read successfully.
	//
	// DataFileName returns the filename (NOT full
	// path or URL) of the file; prefer the original
	// filename if it originated as a file. If the
	// filename is not unique on disk when downloaded,
	// it will be made unique by modifying it. If
	// this value is nil/empty, a filename will be
	// generated from the item's other data.
	//
	// DataFileReader returns a way to read the data.
	// It will be closed when the read is completed.
	//
	// DataFileHash returns the checksum of the
	// content as provided by the service. If the
	// service (or data source) does not provide a
	// hash, leave this field empty, but note that
	// later it will be impossible to efficiently
	// know whether the content has changed on the
	// service from what is stored locally.
	//
	// DataFileMIMEType returns the MIME type of
	// the data file, if known.
	DataFileName() *string
	DataFileReader() (io.ReadCloser, error)
	DataFileHash() []byte
	DataFileMIMEType() *string

	// Metadata returns any optional metadata.
	// Feel free to leave as many fields empty
	// as you'd like: the less fields that are
	// filled out, the smaller the storage size.
	// Metadata is not indexed by the DB but is
	// rendered in projections and queries
	// according to the item's classification.
	Metadata() (*Metadata, error)

	// Location returns an item's location,
	// if known. For now, only Earth
	// coordinates are accepted, but we can
	// improve this later.
	Location() (*Location, error)
}

// ItemClass classifies an item.
type ItemClass int

// Various classes of items.
const (
	ClassUnknown ItemClass = iota
	ClassImage
	ClassVideo
	ClassAudio
	ClassPost
	ClassLocation
	ClassEmail
	ClassPrivateMessage
)

// These are the standard relationships that Timeliner
// recognizes. Using these known relationships is not
// required, but it makes it easier to translate them to
// human-friendly phrases when visualizing the timeline.
var (
	RelReplyTo  = Relation{Label: "reply_to", Bidirectional: false} // "<from> is in reply to <to>"
	RelAttached = Relation{Label: "attached", Bidirectional: true}  // "<to|from> is attached to <from|to>"
	RelQuotes   = Relation{Label: "quotes", Bidirectional: false}   // "<from> quotes <to>"
)

// ItemRow has the structure of an item's row in our DB.
type ItemRow struct {
	ID         int64
	AccountID  int64
	OriginalID string
	PersonID   int64
	Timestamp  time.Time
	Stored     time.Time
	Modified   *time.Time
	Class      ItemClass
	MIMEType   *string
	DataText   *string
	DataFile   *string
	DataHash   *string // base64-encoded SHA-256
	Metadata   *Metadata
	Location

	metaGob []byte // use Metadata.(encode/decode)
}

// Location contains location information.
type Location struct {
	Latitude  *float64
	Longitude *float64
}

// ItemGraph is an item with optional connections to other items.
// All ItemGraph values should be pointers to ensure consistency.
// The usual weird/fun thing about representing graph data structures
// in memory is that a graph is a node, and a node is a graph. 🤓
type ItemGraph struct {
	// The node item. This can be nil, but note that
	// Edges will not be traversed if Node is nil,
	// because there must be a node on both ends of
	// an edge.
	//
	// Optional.
	Node Item

	// Edges are represented as 1:many relations
	// to other "graphs" (nodes in the graph).
	// Fill this out to add multiple items to the
	// timeline at once, while drawing the
	// designated relationships between them.
	// Useful when processing related items in
	// batches.
	//
	// If the items involved in a relationship are
	// not efficiently available at the same time
	// (i.e. if loading both items involved in the
	// relationship would take a non-trivial amount
	// of time or API calls), you can use the
	// Relations field instead, but only after the
	// items have been added to the timeline.
	//
	// Optional.
	Edges map[*ItemGraph][]Relation

	// If items in the graph belong to a collection,
	// specify them here. If the collection does not
	// exist (by row ID or AccountID+OriginalID), it
	// will be created. If it already exists, the
	// collection in the DB will be unioned with the
	// collection specified here. Collections are
	// processed regardless of Node and Edges.
	//
	// Optional.
	Collections []Collection

	// Relationships between existing items in the
	// timeline can be represented here in a list
	// of item IDs that are connected by a label.
	// This field is useful when relationships and
	// the items involved in them are not discovered
	// at the same time. Relations in this list will
	// be added to the timeline, joined by the item
	// IDs described in the RawRelations, only if
	// the items having those IDs (as provided by
	// the data source; we're not talking about DB
	// row IDs here) already exist in the timeline.
	// In other words, this is a best-effort field;
	// useful for forming relationships of existing
	// items, but without access to the actual items
	// themselves. If you have the items involved in
	// the relationships, use Edges instead.
	//
	// Optional.
	Relations []RawRelation
}

// NewItemGraph returns a new node/graph.
func NewItemGraph(node Item) *ItemGraph {
	return &ItemGraph{
		Node:  node,
		Edges: make(map[*ItemGraph][]Relation),
	}
}

// Add adds item to the graph ig by making an edge described
// by rel from the node ig to a new node for item.
//
// This method is for simple inserts, where the only thing to add
// to the graph at this moment is a single item, since the graph
// it inserts contains only a single node populated by item. To
// add a full graph with multiple items (i.e. a graph with edges),
// call ig.Connect directly.
func (ig *ItemGraph) Add(item Item, rel Relation) {
	ig.Connect(NewItemGraph(item), rel)
}

// Connect is a simple convenience function that adds a graph (node)
// to ig by an edge described by rel.
func (ig *ItemGraph) Connect(node *ItemGraph, rel Relation) {
	if ig.Edges == nil {
		ig.Edges = make(map[*ItemGraph][]Relation)
	}
	ig.Edges[node] = append(ig.Edges[node], rel)
}

// RawRelation represents a relationship between
// two items from the same data source (but not
// necessarily the same accounts; assuming that
// a data source's item IDs are globally unique
// across accounts). The item IDs should be those
// which are assigned/provided by the data source,
// NOT a database row ID.
type RawRelation struct {
	FromItemID string
	ToItemID   string
	Relation
}

// Relation describes how two nodes in a graph are related.
// It's essentially an edge on a graph.
type Relation struct {
	Label         string
	Bidirectional bool
}

// Collection represents a group of items.
type Collection struct {
	// The ID of the collection as given
	// by the service; for example, the
	// album ID. If the service does not
	// provide an ID for the collection,
	// invent one such that the next time
	// the collection is encountered and
	// processed, its ID will be the same.
	// An ID is necessary here to ensure
	// uniqueness.
	//
	// REQUIRED.
	OriginalID string

	// The name of the collection as
	// given by the service; for example,
	// the album title.
	//
	// Optional.
	Name *string

	// The description, caption, or any
	// other relevant text describing
	// the collection.
	//
	// Optional.
	Description *string

	// The items for the collection;
	// if ordering is significant,
	// specify each item's Position
	// field; the order of elememts
	// of this slice will not be
	// considered important.
	Items []CollectionItem
}

// CollectionItem represents an item
// stored in a collection.
type CollectionItem struct {
	// The item to add to the collection.
	Item Item

	// Specify if ordering is important.
	Position int

	// Used when processing; this will
	// store the row ID of the item
	// after the item has been inserted
	// into the DB.
	itemRowID int64
}

// Metadata is a unified structure for storing
// item metadata in the DB.
type Metadata struct {
	// A hash or etag provided by the service to
	// make it easy to know if it has changed
	ServiceHash []byte

	// Locations
	LocationAccuracy int
	Altitude         int // meters
	AltitudeAccuracy int
	Heading          int // degrees
	Velocity         int

	// Photos and videos
	EXIF map[string]interface{}
	// TODO: Should we have some of the "most important" EXIF fields explicitly here?

	Width  int
	Height int

	// TODO: Google Photos (how many of these belong in EXIF?)
	CameraMake      string
	CameraModel     string
	FocalLength     float64
	ApertureFNumber float64
	ISOEquivalent   int
	ExposureTime    time.Duration

	FPS float64 // Frames Per Second

	// Posts (Facebook so far)
	Link        string
	Description string
	Name        string
	ParentID    string
	StatusType  string
	Type        string
}

func (m *Metadata) encode() ([]byte, error) {
	// then encode the actual data, and trim off
	// schema from the beginning
	buf := new(bytes.Buffer)
	err := gob.NewEncoder(buf).Encode(m)
	if err != nil {
		return nil, err
	}
	return buf.Bytes()[len(metadataGobPrefix):], nil
}

func (m *Metadata) decode(b []byte) error {
	if b == nil {
		return nil
	}
	fullGob := append(metadataGobPrefix, b...)
	return gob.NewDecoder(bytes.NewReader(fullGob)).Decode(m)
}

var metadataGobPrefix []byte
