package service

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

func A() {
	db, err := sql.Open("sqlite3", "./foo.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
}
