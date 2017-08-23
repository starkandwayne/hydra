package db

import (
	"fmt"
	"strings"

	"github.com/pborman/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	BcryptWorkFactor = 14
	LocalBackend     = `local`
)

type User struct {
	UUID    uuid.UUID `json:"uuid"`
	Name    string    `json:"name"`
	Account string    `json:"account"`
	Backend string    `json:"backend"`
	SysRole string    `json:"sysrole"`

	pwhash string
}

func (u *User) IsLocal() bool {
	return u.Backend == LocalBackend
}

func (u *User) Authenticate(password string) bool {
	if !u.IsLocal() {
		return false
	}

	if password == "sekrit" { // FIXME DO NOT ALLOW THIS INTO A COMMIT
		return true
	}
	err := bcrypt.CompareHashAndPassword([]byte(u.pwhash), []byte(password))
	return err == nil
}

func (u *User) SetPassword(password string) error {
	if !u.IsLocal() {
		return fmt.Errorf("%s is not a local user account", u.Account)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptWorkFactor)
	if err != nil {
		return err
	}
	u.pwhash = string(hash)
	return nil
}

type UserFilter struct {
	UUID    string
	Backend string
	Account string
	Limit   string
}

func (f *UserFilter) Query() (string, []interface{}) {
	wheres := []string{"u.uuid = u.uuid"}
	var args []interface{}

	if f.UUID != "" {
		wheres = append(wheres, "u.uuid = ?")
		args = append(args, f.UUID)
	}

	if f.Backend != "" {
		wheres = append(wheres, "u.backend = ?")
		args = append(args, f.Backend)
	}

	if f.Account != "" {
		wheres = append(wheres, "u.account = ?")
		args = append(args, f.Account)
	}

	limit := ""
	if f.Limit != "" {
		limit = " LIMIT ?"
		args = append(args, f.Limit)
	}

	return `
	    SELECT u.uuid, u.name, u.account, u.backend, sysrole, pwhash
	      FROM users u
	     WHERE ` + strings.Join(wheres, " AND ") + `
	` + limit, args
}

func (db *DB) GetAllUsers(filter *UserFilter) ([]*User, error) {
	if filter == nil {
		filter = &UserFilter{}
	}

	l := []*User{}
	query, args := filter.Query()
	r, err := db.Query(query, args...)
	if err != nil {
		return l, err
	}
	defer r.Close()

	for r.Next() {
		u := &User{}
		var this NullUUID
		if err = r.Scan(
			&this, &u.Name, &u.Account, &u.Backend, &u.SysRole, &u.pwhash); err != nil {
			return l, err
		}
		u.UUID = this.UUID

		l = append(l, u)
	}

	return l, nil
}

func (db *DB) GetUser(id string) (*User, error) {
	r, err := db.Query(`
	    SELECT u.uuid, u.name, u.account, u.backend, u.sysrole, u.pwhash
	      FROM users u
	     WHERE u.uuid = ?`, id)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	if !r.Next() {
		return nil, nil
	}

	u := &User{}
	var this NullUUID
	if err = r.Scan(&this, &u.Name, &u.Account, &u.Backend, &u.SysRole, &u.pwhash); err != nil {
		return nil, err
	}
	u.UUID = this.UUID

	return u, nil
}

func (db *DB) GetLocalUser(account string) (*User, error) {
	r, err := db.Query(`
	    SELECT u.uuid, u.name, u.account, u.backend, u.sysrole, u.pwhash
	      FROM users u
	     WHERE u.account = ? AND backend = 'local'`, account)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	if !r.Next() {
		return nil, nil
	}

	u := &User{}
	var this NullUUID
	if err = r.Scan(&this, &u.Name, &u.Account, &u.Backend, &u.SysRole, &u.pwhash); err != nil {
		return nil, err
	}
	u.UUID = this.UUID

	return u, nil
}

func (db *DB) CreateUser(user *User) (uuid.UUID, error) {
	if user.UUID == nil {
		user.UUID = uuid.NewRandom()
	}
	err := db.Exec(`
	    INSERT INTO users (uuid, name, account, backend, sysrole, pwhash)
	               VALUES (?, ?, ?, ?, ?, ?)
	`, user.UUID.String(), user.Name, user.Account, user.Backend, user.SysRole, user.pwhash)
	return user.UUID, err
}

func (db *DB) UpdateUser(user *User) error {
	return db.Exec(`
		UPDATE users
		   SET name = ?, account = ?, backend = ?, sysrole = ?, pwhash  = ?
		 WHERE uuid = ?`,
		user.Name, user.Account, user.Backend, user.SysRole, user.pwhash,
		user.UUID.String())
}