// cmd/migrate applies migrations/*.sql via goose's library API against the
// embedded FS (migrations.FS) — no `goose` CLI needed at runtime, which
// matters for the Docker image (deploy/Dockerfile's migrate stage has
// nothing else installed). Local dev can keep using the goose CLI directly
// against the migrations/ directory exactly as before; this binary is an
// additional, container-friendly way to run the same SQL, not a replacement.
package main

import (
	"database/sql"
	"flag"
	"log"

	_ "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"

	"aigc-platform/internal/pkg/config"
	"aigc-platform/migrations"
)

func main() {
	command := flag.String("command", "up", "migration action: up, down, down-to, redo, status, version")
	version := flag.Int64("version", 0, "target version for down-to")
	flag.Parse()

	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("mysql"); err != nil {
		log.Fatalf("set dialect: %v", err)
	}
	switch *command {
	case "up":
		err = goose.Up(db, ".")
	case "down":
		err = goose.Down(db, ".")
	case "down-to":
		if *version < 0 {
			log.Fatal("-version must be non-negative")
		}
		err = goose.DownTo(db, ".", *version)
	case "redo":
		err = goose.Redo(db, ".")
	case "status":
		err = goose.Status(db, ".")
	case "version":
		err = goose.Version(db, ".")
	default:
		log.Fatalf("unknown -command %q (want up, down, down-to, redo, status, version)", *command)
	}
	if err != nil {
		log.Fatalf("migrate %s: %v", *command, err)
	}
	log.Printf("migration %s complete", *command)
}
