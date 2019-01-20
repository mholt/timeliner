package timeliner

import (
	"database/sql"
	"fmt"
)

// getPerson returns the person mapped to userID on service.
// If the person does not exist, it is created.
func (t *Timeline) getPerson(dataSourceID, userID, name string) (Person, error) {
	// first, load the person
	var p Person
	err := t.db.QueryRow(`SELECT persons.id, persons.name
		FROM persons, person_identities
		WHERE person_identities.data_source_id=?
			AND person_identities.user_id=?
			AND persons.id = person_identities.person_id
		LIMIT 1`, dataSourceID, userID).Scan(&p.ID, &p.Name)
	if err == sql.ErrNoRows {
		// person does not exist; create this mapping - TODO: do in a transaction
		p = Person{Name: name}
		res, err := t.db.Exec(`INSERT INTO persons (name) VALUES (?)`, p.Name)
		if err != nil {
			return Person{}, fmt.Errorf("adding new person: %v", err)
		}
		p.ID, err = res.LastInsertId()
		if err != nil {
			return Person{}, fmt.Errorf("getting person ID: %v", err)
		}
		_, err = t.db.Exec(`INSERT INTO person_identities
			(person_id, data_source_id, user_id) VALUES (?, ?, ?)`,
			p.ID, dataSourceID, userID)
		if err != nil {
			return Person{}, fmt.Errorf("adding new person identity mapping: %v", err)
		}
	} else if err != nil {
		return Person{}, fmt.Errorf("selecting person identity: %v", err)
	}

	// now get all the person's identities
	rows, err := t.db.Query(`SELECT id, person_id, data_source_id, user_id
		FROM person_identities WHERE person_id=?`, p.ID)
	if err != nil {
		return Person{}, fmt.Errorf("selecting person's known identities: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ident PersonIdentity
		err := rows.Scan(&ident.ID, &ident.PersonID, &ident.DataSourceID, &ident.UserID)
		if err != nil {
			return Person{}, fmt.Errorf("loading person's identity: %v", err)
		}
		p.Identities = append(p.Identities, ident)
	}
	if err = rows.Err(); err != nil {
		return Person{}, fmt.Errorf("scanning identity rows: %v", err)
	}

	return p, nil
}

// Person represents a person.
type Person struct {
	ID         int64
	Name       string
	Identities []PersonIdentity
}

// PersonIdentity is a way to map a user ID on a service to a person.
type PersonIdentity struct {
	ID           int64
	PersonID     string
	DataSourceID string
	UserID       string
}
