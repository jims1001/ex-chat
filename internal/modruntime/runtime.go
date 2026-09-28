package modruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const FoundationVersion = "foundation-v1"

type Config struct {
	ModuleID        string
	Address         string
	DBDriver        string
	DBPath          string
	DBDSN           string
	ContractVersion string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		ModuleID:        strings.ToUpper(strings.TrimSpace(getenv("MODULE_ID"))),
		Address:         strings.TrimSpace(getenv("MODULE_ADDR")),
		DBDriver:        strings.ToLower(strings.TrimSpace(getenv("MODULE_DB_DRIVER"))),
		DBPath:          strings.TrimSpace(getenv("MODULE_DB_PATH")),
		DBDSN:           strings.TrimSpace(getenv("MODULE_DB_DSN")),
		ContractVersion: strings.TrimSpace(getenv("MODULE_CONTRACT_VERSION")),
	}
	if cfg.Address == "" {
		cfg.Address = ":8090"
	}
	if cfg.ContractVersion == "" {
		cfg.ContractVersion = "v1"
	}
	if _, err := database.ModuleModels(cfg.ModuleID); err != nil {
		return Config{}, err
	}
	switch cfg.DBDriver {
	case "sqlite":
		if cfg.DBPath == "" {
			return Config{}, errors.New("MODULE_DB_PATH is required for sqlite")
		}
	case "postgres":
		if cfg.DBDSN == "" {
			return Config{}, errors.New("MODULE_DB_DSN is required for postgres")
		}
	default:
		return Config{}, errors.New("MODULE_DB_DRIVER must be sqlite or postgres")
	}
	return cfg, nil
}

func OpenDatabase(cfg Config) (*gorm.DB, error) {
	var dialector gorm.Dialector
	if cfg.DBDriver == "postgres" {
		dialector = postgres.Open(cfg.DBDSN)
	} else {
		dialector = sqlite.Open(cfg.DBPath)
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, errors.New("module database connection failed")
	}
	return db, nil
}

type Runtime struct {
	Config    Config
	DB        *gorm.DB
	StartedAt time.Time
}

func (r Runtime) Readiness() error {
	if r.DB == nil {
		return errors.New("module database unavailable")
	}
	conn, err := r.DB.DB()
	if err != nil {
		return errors.New("module database unavailable")
	}
	if err := conn.Ping(); err != nil {
		return errors.New("module database unavailable")
	}
	models, err := database.ModuleModels(r.Config.ModuleID)
	if err != nil {
		return err
	}
	for _, model := range models {
		if !r.DB.Migrator().HasTable(model) {
			return fmt.Errorf("module table missing: %s", reflect.TypeOf(model).Elem().Name())
		}
	}
	return nil
}

func (r Runtime) Handler() http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, status int, payload any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, map[string]any{"status": "up", "module_id": r.Config.ModuleID, "foundation_version": FoundationVersion, "contract_version": r.Config.ContractVersion})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if err := r.Readiness(); err != nil {
			write(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "module_id": r.Config.ModuleID, "reason": err.Error()})
			return
		}
		write(w, http.StatusOK, map[string]any{"status": "ready", "module_id": r.Config.ModuleID, "foundation_version": FoundationVersion, "contract_version": r.Config.ContractVersion})
	})
	return mux
}

func FromEnvironment() (Runtime, error) {
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		return Runtime{}, err
	}
	db, err := OpenDatabase(cfg)
	if err != nil {
		return Runtime{}, err
	}
	return Runtime{Config: cfg, DB: db, StartedAt: time.Now().UTC()}, nil
}
