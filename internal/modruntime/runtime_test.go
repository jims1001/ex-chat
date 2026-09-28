package modruntime

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
)

func TestEveryModuleStartsWithIndependentConfigAndReadiness(t *testing.T) {
	for _, module := range database.ModuleIDs() {
		t.Run(module, func(t *testing.T) {
			values := map[string]string{"MODULE_ID": module, "MODULE_ADDR": ":0", "MODULE_DB_DRIVER": "sqlite", "MODULE_DB_PATH": filepath.Join(t.TempDir(), module+".db"), "MODULE_CONTRACT_VERSION": "v1", "MODULE_AUTH_TOKEN": "test-secret", "MODULE_ALLOWED_CALLERS": "ORC,API"}
			cfg, err := LoadConfig(func(key string) string { return values[key] })
			if err != nil {
				t.Fatal(err)
			}
			db, err := OpenDatabase(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := database.AutoMigrateModule(db, module); err != nil {
				t.Fatal(err)
			}
			runtime := Runtime{Config: cfg, DB: db}
			for _, path := range []string{"/health", "/ready"} {
				response := httptest.NewRecorder()
				runtime.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusOK {
					t.Fatalf("%s %s status=%d body=%s", module, path, response.Code, response.Body.String())
				}
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body["module_id"] != module {
					t.Fatalf("module identity mismatch: %v", body)
				}
			}
		})
	}
}

func TestRuntimeBindsIndependentListener(t *testing.T) {
	values := map[string]string{"MODULE_ID": "IAM", "MODULE_DB_DRIVER": "sqlite", "MODULE_DB_PATH": filepath.Join(t.TempDir(), "iam.db"), "MODULE_AUTH_TOKEN": "test-secret", "MODULE_ALLOWED_CALLERS": "TEN,API"}
	cfg, err := LoadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrateModule(db, cfg.ModuleID); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: Runtime{Config: cfg, DB: db}.Handler()}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	response, err := http.Get("http://" + listener.Addr().String() + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("listener readiness status=%d", response.StatusCode)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("module listener did not stop")
	}
}

func TestReadinessRejectsMissingSchemaAndSharedConfigFallback(t *testing.T) {
	values := map[string]string{"MODULE_ID": "CUS", "MODULE_DB_DRIVER": "sqlite", "DB_PATH": "shared.db", "MODULE_AUTH_TOKEN": "test-secret", "MODULE_ALLOWED_CALLERS": "CON"}
	if _, err := LoadConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("shared DB_PATH was accepted without MODULE_DB_PATH")
	}
	values["MODULE_DB_PATH"] = filepath.Join(t.TempDir(), "cus.db")
	cfg, err := LoadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{Config: cfg, DB: db}
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing schema readiness=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got == "" {
		t.Fatal(fmt.Errorf("empty readiness response"))
	}
}

func TestLoadConfigValidatesEventFanoutTargets(t *testing.T) {
	values := map[string]string{
		"MODULE_ID": "CUS", "MODULE_DB_DRIVER": "sqlite", "MODULE_DB_PATH": filepath.Join(t.TempDir(), "cus.db"),
		"MODULE_AUTH_TOKEN": "test-secret", "MODULE_ALLOWED_CALLERS": "API", "MODULE_EVENT_POLL_INTERVAL_MS": "25",
		"MODULE_EVENT_TARGETS_JSON": `{"CON":{"dependency":{"url":"http://con:8090","token":"con-secret"},"event_types":{"cus.contact.merged.v1":true}}}`,
	}
	cfg, err := LoadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventPollInterval != 25*time.Millisecond || !cfg.EventTargets["CON"].EventTypes["cus.contact.merged.v1"] {
		t.Fatalf("event fanout config=%+v interval=%s", cfg.EventTargets, cfg.EventPollInterval)
	}
	values["MODULE_EVENT_TARGETS_JSON"] = `{"con":{"dependency":{"url":"http://con:8090","token":"con-secret"},"event_types":{"cus.contact.merged.v1":true}}}`
	if _, err := LoadConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("lowercase event target was accepted")
	}
	values["MODULE_EVENT_TARGETS_JSON"] = `{"CON":{"dependency":{"url":"http://con:8090","token":"con-secret"},"event_types":{"msg.message.created.v1":true}}}`
	if _, err := LoadConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("foreign source event type was accepted")
	}
}
