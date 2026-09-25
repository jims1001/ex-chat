// module-migrate prepares a module's schema in an explicitly selected database.
// It never starts the combined HTTP router or copies existing business data.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func run(args []string, getenv func(string) string, output io.Writer) error {
	flags := flag.NewFlagSet("module-migrate", flag.ContinueOnError)
	flags.SetOutput(output)
	module := flags.String("module", "", "logical module ID, e.g. MSG")
	path := flags.String("sqlite-path", "", "explicit path to the isolated SQLite database")
	pg := flags.Bool("postgres", false, "use MODULE_DB_DSN for an isolated PostgreSQL database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if _, err := database.ModuleModels(*module); err != nil {
		return err
	}
	if (*path != "") == *pg {
		return errors.New("select exactly one of -sqlite-path or -postgres")
	}
	var dialector gorm.Dialector
	if *pg {
		dsn := getenv("MODULE_DB_DSN")
		if dsn == "" {
			return errors.New("MODULE_DB_DSN is required")
		}
		dialector = postgres.Open(dsn)
	} else {
		dialector = sqlite.Open(*path)
	}
	// Connection errors can contain credentials; do not print driver errors.
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return errors.New("module database connection failed")
	}
	conn, err := db.DB()
	if err != nil {
		return errors.New("module database connection unavailable")
	}
	defer conn.Close()
	if err := database.AutoMigrateModule(db, *module); err != nil {
		return errors.New("module schema migration failed")
	}
	fmt.Fprintf(output, "%s schema prepared; business runtime extraction is not verified\n", *module)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
