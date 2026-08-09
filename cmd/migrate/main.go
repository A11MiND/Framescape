// cmd/migrate applies migrations/*.sql via goose's library API against the
// embedded FS (migrations.FS) — no `goose` CLI needed at runtime, which
// matters for the Docker image (deploy/Dockerfile's migrate stage has
// nothing else installed). Local dev can keep using the goose CLI directly
// against the migrations/ directory exactly as before; this binary is an
// additional, container-friendly way to run the same SQL, not a replacement.
package main

import (
	"database/sql"
	"log"

	_ "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"

	"aigc-platform/internal/pkg/config"
	"aigc-platform/migrations"
)

func main() {
	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("mysql"); err != nil {
		log.Fatalf("set dialect: %v", err)
	}
	if err := goose.Up(db, "."); err != nil {
		log.Fatalf("migrate up: %v", err)
	}
	log.Println("migrations applied")
}
