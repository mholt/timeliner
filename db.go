package timeliner

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// register the sqlite3 driver
	_ "github.com/mattn/go-sqlite3"
)

func openDB(dataDir string) (*sql.DB, error) {
	var db *sql.DB
	var err error
	defer func() {
		if err != nil && db != nil {
			db.Close()
		}
	}()

	err = os.MkdirAll(dataDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("making data directory: %v", err)
	}

	dbPath := filepath.Join(dataDir, "index.db")

	db, err = sql.Open("sqlite3", dbPath+"?_foreign_keys=true")
	if err != nil {
		return nil, fmt.Errorf("opening database: %v", err)
	}

	// ensure DB is provisioned
	_, err = db.Exec(createDB)
	if err != nil {
		return nil, fmt.Errorf("setting up database: %v", err)
	}

	// add all registered data sources
	err = saveAllDataSources(db)
	if err != nil {
		return nil, fmt.Errorf("saving registered data sources to database: %v", err)
	}

	return db, nil
}

const createDB = `
-- A data source is a content provider, like a cloud photo service, social media site, or exported archive format.
CREATE TABLE IF NOT EXISTS "data_sources" (
	"id" TEXT PRIMARY KEY,
	"name" TEXT NOT NULL
);

-- An account contains credentials necessary for accessing a data source.
CREATE TABLE IF NOT EXISTS "accounts" (
	"id" INTEGER PRIMARY KEY,
	"data_source_id" TEXT NOT NULL,
	"user_id" TEXT NOT NULL,
	"authorization" BLOB,
	"checkpoint" BLOB,
	"last_item_id" INTEGER, -- row ID of item having highest timestamp processed during the last run
	FOREIGN KEY ("data_source_id") REFERENCES "data_sources"("id") ON DELETE CASCADE,
	FOREIGN KEY ("last_item_id") REFERENCES "items"("id") ON DELETE SET NULL,
	UNIQUE ("data_source_id", "user_id")
);

CREATE TABLE IF NOT EXISTS "persons" (
	"id" INTEGER PRIMARY KEY,
	"name" TEXT
);

-- This table specifies identities (user IDs, etc.) of a person across data_sources.
CREATE TABLE IF NOT EXISTS "person_identities" (
	"id" INTEGER PRIMARY KEY,
	"person_id" INTEGER NOT NULL,
	"data_source_id" TEXT NOT NULL,
	"user_id" TEXT NOT NULL, -- whatever identifier a person takes on at the data source
	FOREIGN KEY ("person_id") REFERENCES "persons"("id") ON DELETE CASCADE,
	FOREIGN KEY ("data_source_id") REFERENCES "data_sources"("id") ON DELETE CASCADE,
	UNIQUE ("person_id", "data_source_id", "user_id")
);

-- An item is something downloaded from a specific account on a specific data source.
CREATE TABLE IF NOT EXISTS "items" (
	"id" INTEGER PRIMARY KEY,
	"account_id" INTEGER NOT NULL,
	"original_id" TEXT NOT NULL, -- ID provided by the data source
	"person_id" INTEGER NOT NULL,
	"timestamp" INTEGER, -- timestamp when item content was originally created (NOT when the database row was created)
	"stored" INTEGER NOT NULL DEFAULT (strftime('%s', CURRENT_TIME)), -- timestamp row was created or last updated from source
	"modified" INTEGER, -- timestamp when item was locally modified; if not null, then item is "not clean"
	"class" INTEGER,
	"mime_type" TEXT,
	"data_text" TEXT COLLATE NOCASE,  -- item content, if text-encoded
	"data_file" TEXT, -- item filename, if non-text or not suitable for storage in DB (usually media items)
	"data_hash" TEXT, -- base64 encoding of SHA-256 checksum of contents of data file, if any
	"metadata" BLOB,  -- optional extra information
	"latitude" REAL,
	"longitude" REAL,
	FOREIGN KEY ("account_id") REFERENCES "accounts"("id") ON DELETE CASCADE,
	FOREIGN KEY ("person_id") REFERENCES "persons"("id") ON DELETE CASCADE,
	UNIQUE ("original_id", "account_id")
);

CREATE INDEX IF NOT EXISTS "idx_items_timestamp" ON "items"("timestamp");
CREATE INDEX IF NOT EXISTS "idx_items_data_text" ON "items"("data_text");
CREATE INDEX IF NOT EXISTS "idx_items_data_file" ON "items"("data_file");
CREATE INDEX IF NOT EXISTS "idx_items_data_hash" ON "items"("data_hash");

-- Relationships draws relationships between and across items and persons.
CREATE TABLE IF NOT EXISTS "relationships" (
	"id" INTEGER PRIMARY KEY,
	"from_person_id" INTEGER,
	"from_item_id" INTEGER,
	"to_person_id" INTEGER,
	"to_item_id" INTEGER,
	"directed" BOOLEAN, -- if false, the edge goes both ways
 	"label" TEXT NOT NULL,
	FOREIGN KEY ("from_item_id") REFERENCES "items"("id") ON DELETE CASCADE,
	FOREIGN KEY ("to_item_id") REFERENCES "items"("id") ON DELETE CASCADE,
	FOREIGN KEY ("from_person_id") REFERENCES "persons"("id") ON DELETE CASCADE,
	FOREIGN KEY ("to_person_id") REFERENCES "persons"("id") ON DELETE CASCADE,
	UNIQUE ("from_item_id", "to_item_id", "label"),
	UNIQUE ("from_person_id", "to_person_id", "label"),
	UNIQUE ("from_item_id", "to_person_id", "label"),
	UNIQUE ("from_person_id", "to_item_id", "label")
);

CREATE TABLE IF NOT EXISTS "collections" (
	"id" INTEGER PRIMARY KEY,
	"account_id" INTEGER NOT NULL,
	"original_id" TEXT,
	"name" TEXT,
	"description" TEXT,
	"modified" INTEGER, -- timestamp when collection or any of its items/ordering were modified locally; if not null, then collection is "not clean"
	FOREIGN KEY ("account_id") REFERENCES "accounts"("id") ON DELETE CASCADE,
	UNIQUE("account_id", "original_id")
);

CREATE TABLE IF NOT EXISTS "collection_items" (
	"id" INTEGER PRIMARY KEY,
	"item_id" INTEGER NOT NULL,
	"collection_id" INTEGER NOT NULL,
	"position" INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY ("item_id") REFERENCES "items"("id") ON DELETE CASCADE,
	FOREIGN KEY ("collection_id") REFERENCES "collections"("id") ON DELETE CASCADE,
	UNIQUE("item_id", "collection_id", "position")
);
`
