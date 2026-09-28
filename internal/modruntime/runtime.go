package modruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strconv"
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
	ModuleID         string
	Address          string
	DBDriver         string
	DBPath           string
	DBDSN            string
	ContractVersion  string
	ReleaseVersion   string
	AcceptedVersions map[string]bool
	AuthToken        string
	AllowedCallers   map[string]bool
	MaxTimeout       time.Duration
	MaxRequestBytes  int64
	EnableBusiness   bool
	Dependencies     map[string]DependencyConfig
}

type DependencyConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		ModuleID:        strings.ToUpper(strings.TrimSpace(getenv("MODULE_ID"))),
		Address:         strings.TrimSpace(getenv("MODULE_ADDR")),
		DBDriver:        strings.ToLower(strings.TrimSpace(getenv("MODULE_DB_DRIVER"))),
		DBPath:          strings.TrimSpace(getenv("MODULE_DB_PATH")),
		DBDSN:           strings.TrimSpace(getenv("MODULE_DB_DSN")),
		ContractVersion: strings.TrimSpace(getenv("MODULE_CONTRACT_VERSION")),
		ReleaseVersion:  strings.TrimSpace(getenv("MODULE_RELEASE_VERSION")),
		AuthToken:       strings.TrimSpace(getenv("MODULE_AUTH_TOKEN")),
		AllowedCallers:  parseCallers(getenv("MODULE_ALLOWED_CALLERS")),
		EnableBusiness:  strings.EqualFold(strings.TrimSpace(getenv("MODULE_ENABLE_BUSINESS_CONTRACTS")), "true"),
		Dependencies:    map[string]DependencyConfig{},
	}
	if raw := strings.TrimSpace(getenv("MODULE_DEPENDENCIES_JSON")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Dependencies); err != nil {
			return Config{}, errors.New("MODULE_DEPENDENCIES_JSON is invalid")
		}
		for module, dependency := range cfg.Dependencies {
			if module != strings.ToUpper(module) || strings.TrimSpace(dependency.URL) == "" || strings.TrimSpace(dependency.Token) == "" {
				return Config{}, errors.New("MODULE_DEPENDENCIES_JSON contains an invalid dependency")
			}
		}
	}
	if cfg.Address == "" {
		cfg.Address = ":8090"
	}
	if cfg.ContractVersion == "" {
		cfg.ContractVersion = "v1"
	}
	if cfg.ReleaseVersion == "" {
		cfg.ReleaseVersion = "release-1"
	}
	cfg.AcceptedVersions = parseVersions(getenv("MODULE_ACCEPTED_CONTRACT_VERSIONS"))
	if len(cfg.AcceptedVersions) == 0 {
		cfg.AcceptedVersions[cfg.ContractVersion] = true
	}
	if !cfg.AcceptedVersions[cfg.ContractVersion] {
		return Config{}, errors.New("MODULE_ACCEPTED_CONTRACT_VERSIONS must include MODULE_CONTRACT_VERSION")
	}
	if cfg.AuthToken == "" {
		return Config{}, errors.New("MODULE_AUTH_TOKEN is required")
	}
	if len(cfg.AllowedCallers) == 0 {
		return Config{}, errors.New("MODULE_ALLOWED_CALLERS is required")
	}
	cfg.MaxTimeout = 10 * time.Second
	if value := strings.TrimSpace(getenv("MODULE_MAX_TIMEOUT_MS")); value != "" {
		milliseconds, err := strconv.Atoi(value)
		if err != nil || milliseconds <= 0 || milliseconds > 60000 {
			return Config{}, errors.New("MODULE_MAX_TIMEOUT_MS must be between 1 and 60000")
		}
		cfg.MaxTimeout = time.Duration(milliseconds) * time.Millisecond
	}
	cfg.MaxRequestBytes = 1 << 20
	if value := strings.TrimSpace(getenv("MODULE_MAX_REQUEST_BYTES")); value != "" {
		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil || bytes <= 0 || bytes > 10<<20 {
			return Config{}, errors.New("MODULE_MAX_REQUEST_BYTES must be between 1 and 10485760")
		}
		cfg.MaxRequestBytes = bytes
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

func parseVersions(value string) map[string]bool {
	versions := map[string]bool{}
	for _, version := range strings.Split(value, ",") {
		version = strings.TrimSpace(version)
		if version != "" {
			versions[version] = true
		}
	}
	return versions
}

func parseCallers(value string) map[string]bool {
	callers := map[string]bool{}
	for _, caller := range strings.Split(value, ",") {
		caller = strings.ToUpper(strings.TrimSpace(caller))
		if caller != "" {
			callers[caller] = true
		}
	}
	return callers
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
	Contracts *ContractRegistry
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
	if r.Contracts != nil && r.DB != nil {
		r.Contracts.UseCommandReceipts(r.DB)
	}
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, status int, payload any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, map[string]any{"status": "up", "module_id": r.Config.ModuleID, "foundation_version": FoundationVersion, "contract_version": r.Config.ContractVersion, "release_version": r.Config.ReleaseVersion})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		if err := r.Readiness(); err != nil {
			write(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "module_id": r.Config.ModuleID, "reason": err.Error()})
			return
		}
		write(w, http.StatusOK, map[string]any{"status": "ready", "module_id": r.Config.ModuleID, "foundation_version": FoundationVersion, "contract_version": r.Config.ContractVersion})
	})
	if r.Contracts != nil {
		mux.Handle("/contracts/", r.Contracts.Handler(r.Config))
	}
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
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry(cfg.ModuleID), StartedAt: time.Now().UTC()}
	if cfg.EnableBusiness {
		if err := runtime.RegisterBusinessContracts(); err != nil {
			return Runtime{}, err
		}
	}
	return runtime, nil
}
