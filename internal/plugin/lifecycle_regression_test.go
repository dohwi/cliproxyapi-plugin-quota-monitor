package plugin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestLifecycleConfigYAMLAndManagementFirstFill exercises the host-shaped
// lifecycle wire (register/reconfigure config_yaml is a []byte/base64 field)
// with a secret-free auth-dir fixture. It pins the ownership boundary:
// management GET status/accounts performs the first fill, while the resource
// accounts route is a passive snapshot and does not perform privileged work.
func TestLifecycleConfigYAMLAndManagementFirstFill(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	authDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(authDir, "codex-fixture.json"), []byte(`{"proxy_url":"direct","id":"fixture-id","type":"codex"}`), 0600); err != nil {
		t.Fatal(err)
	}

	var authFiles, apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			authFiles.Add(1)
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"fixture@example.invalid","name":"codex-fixture.json","id":"fixture-id","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}]`))
		case "/management/api-call":
			apiCalls.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{\"rate_limit\":{\"primary_window\":{\"used_percent\":20}}}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "fixture-management-key")

	config := []byte("auth-dir: " + authDir + "\n")
	request, err := json.Marshal(struct {
		SchemaVersion uint32 `json:"schema_version"`
		ConfigYAML    []byte `json:"config_yaml"`
	}{SchemaVersion: 6, ConfigYAML: config})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := HandleMethod("plugin.register", request); !ok {
		t.Fatal("plugin.register was not handled")
	}
	// Verify the wire is actually the host's base64 []byte representation,
	// rather than accidentally relying on a JSON string in the fixture.
	var wire struct {
		ConfigYAML string `json:"config_yaml"`
	}
	if err := json.Unmarshal(request, &wire); err != nil {
		t.Fatal(err)
	}
	if decoded, err := base64.StdEncoding.DecodeString(wire.ConfigYAML); err != nil || string(decoded) != string(config) {
		t.Fatalf("config_yaml wire mismatch: decoded=%q err=%v", decoded, err)
	}

	statusRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: mgmtBase + "/status"})
	statusRaw := handleManagementHandle(statusRequest)
	statusCode, status := decodeMgmt(t, statusRaw)
	if statusCode != http.StatusOK || status["configured"] != true || status["accounts"] != float64(1) || status["stale"] != false {
		t.Fatalf("first management status did not fill cache: code=%d body=%#v", statusCode, status)
	}
	health, ok := status["health"].(map[string]any)
	if !ok || health["collections"] != float64(1) || health["probe_attempts"] != float64(1) {
		t.Fatalf("first fill health=%#v", status["health"])
	}
	if authFiles.Load() != 1 || apiCalls.Load() != 1 {
		t.Fatalf("first fill calls: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	resourceRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: resourceBase + "/data/accounts"})
	resourceCode, resource := decodeMgmt(t, handleManagementHandle(resourceRequest))
	if resourceCode != http.StatusOK || resource["count"] != float64(1) || resource["stale"] != false {
		t.Fatalf("resource snapshot=%#v", resource)
	}
	if authFiles.Load() != 1 || apiCalls.Load() != 1 {
		t.Fatalf("resource triggered privileged work: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}
}

// TestManagementReadRetriesAfterReconfigureGenerationChange ensures a status
// request that joined an invalidated old flight performs a fill in the new
// generation instead of exposing the old configuration_changed/empty result.
func TestManagementReadRetriesAfterReconfigureGenerationChange(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	var authFiles atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/management/auth-files" {
			http.NotFound(w, r)
			return
		}
		if authFiles.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		_, _ = w.Write([]byte(`[{"provider":"claude","auth_index":"7","account":"new@example.invalid"}]`))
	}))
	defer server.Close()
	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "fixture-management-key")
	configureFromEnv(nil)

	readRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: mgmtBase + "/accounts"})
	result := make(chan []byte, 1)
	go func() { result <- handleManagementHandle(readRequest) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old management collection did not start")
	}

	registerRequest, _ := json.Marshal(struct {
		SchemaVersion uint32 `json:"schema_version"`
	}{SchemaVersion: 6})
	if _, ok := HandleMethod("plugin.reconfigure", registerRequest); !ok {
		t.Fatal("plugin.reconfigure was not handled")
	}
	close(release)

	select {
	case raw := <-result:
		code, body := decodeMgmt(t, raw)
		if code != http.StatusOK || body["configured"] != true || body["stale"] != false || body["accounts"].([]any) == nil || len(body["accounts"].([]any)) != 1 {
			t.Fatalf("reconfigure-racing management read: code=%d body=%#v", code, body)
		}
	case <-time.After(managementCollectionTimeout):
		t.Fatal("management read did not retry within bounded deadline")
	}
	if authFiles.Load() < 2 {
		t.Fatalf("reconfigure retry did not start a new collection: auth-files=%d", authFiles.Load())
	}
}

// TestManagementInitialCollectionAfterRegisterSequence exercises the real production
// lifecycle sequence: plugin.register -> management.register -> reconfigure -> status.
// It verifies that right after startup/reconfigure, the first collection is reliably
// performed and committed without generation contention, accounts > 0, stale=false,
// collections > 0, and subsequent resource accounts reads remain passive-only.
func TestManagementInitialCollectionAfterRegisterSequence(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	authDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(authDir, "codex-fixture.json"), []byte(`{"proxy_url":"direct","id":"fixture-id","type":"codex"}`), 0600); err != nil {
		t.Fatal(err)
	}

	var authFiles, apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/management/auth-files":
			authFiles.Add(1)
			_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"7","account":"fixture@example.invalid","name":"codex-fixture.json","id":"fixture-id","quota":{"observed_at":"2026-03-30T12:00:00Z","signals":{"x-codex-plan-type":"pro"}}}]`))
		case "/management/api-call":
			apiCalls.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"header":{},"body":"{\"rate_limit\":{\"primary_window\":{\"used_percent\":20}}}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "fixture-management-key")

	config := []byte("auth-dir: " + authDir + "\n")
	regRequest, err := json.Marshal(struct {
		SchemaVersion uint32 `json:"schema_version"`
		ConfigYAML    []byte `json:"config_yaml"`
	}{SchemaVersion: 6, ConfigYAML: config})
	if err != nil {
		t.Fatal(err)
	}

	// 1. plugin.register
	if _, ok := HandleMethod("plugin.register", regRequest); !ok {
		t.Fatal("plugin.register was not handled")
	}

	// 2. management.register
	if _, ok := HandleMethod("management.register", []byte("{}")); !ok {
		t.Fatal("management.register was not handled")
	}

	// 2b. Resource accounts before first management fill must be passive-only (count=0, collections=0, stale=true)
	preResourceRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: resourceBase + "/data/accounts"})
	preCode, preResource := decodeMgmt(t, handleManagementHandle(preResourceRequest))
	if preCode != http.StatusOK || preResource["count"] != float64(0) || preResource["stale"] != true {
		t.Fatalf("pre-fill resource snapshot unexpected: code=%d body=%#v", preCode, preResource)
	}
	if authFiles.Load() != 0 || apiCalls.Load() != 0 {
		t.Fatalf("pre-fill resource triggered upstream calls: auth-files=%d api-calls=%d", authFiles.Load(), apiCalls.Load())
	}

	// 3. reconfigure (testing "reconfigure" method alias without config_yaml: must preserve authDir)
	reconfigRequest, err := json.Marshal(struct {
		SchemaVersion uint32 `json:"schema_version"`
	}{SchemaVersion: 6})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := HandleMethod("reconfigure", reconfigRequest); !ok {
		t.Fatal("reconfigure was not handled")
	}

	// 4. status (authenticated management GET /status right after reconfigure performs first fill)
	statusRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: mgmtBase + "/status"})
	statusRaw := handleManagementHandle(statusRequest)
	statusCode, status := decodeMgmt(t, statusRaw)
	if statusCode != http.StatusOK {
		t.Fatalf("status request status code=%d, want 200", statusCode)
	}
	if status["configured"] != true {
		t.Fatalf("configured=%v, want true", status["configured"])
	}
	if status["stale"] == true {
		t.Fatalf("stale=%v, want false after successful status collection", status["stale"])
	}
	accCount, _ := status["accounts"].(float64)
	if accCount < 1 {
		t.Fatalf("accounts=%v, want >= 1", status["accounts"])
	}
	health, ok := status["health"].(map[string]any)
	if !ok {
		t.Fatalf("health missing or not map: %#v", status["health"])
	}
	colls, _ := health["collections"].(float64)
	if colls < 1 {
		t.Fatalf("health.collections=%v, want >= 1", colls)
	}
	probes, _ := health["probe_attempts"].(float64)
	if probes < 1 {
		t.Fatalf("health.probe_attempts=%v, want >= 1", probes)
	}
	if authFiles.Load() != 1 || apiCalls.Load() != 1 {
		t.Fatalf("initial fill calls: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	// 5. resource accounts snapshot must remain passive-only and reflect committed accounts
	resourceRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: resourceBase + "/data/accounts"})
	resourceCode, resource := decodeMgmt(t, handleManagementHandle(resourceRequest))
	if resourceCode != http.StatusOK || resource["count"] != float64(1) || resource["stale"] != false {
		t.Fatalf("resource snapshot=%#v", resource)
	}
	if authFiles.Load() != 1 || apiCalls.Load() != 1 {
		t.Fatalf("passive resource read triggered upstream work: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	// 6. Test reconfigure followed by POST /refresh: cooldown must NOT block initial fill
	if _, ok := HandleMethod("plugin.reconfigure", reconfigRequest); !ok {
		t.Fatal("plugin.reconfigure was not handled")
	}
	refreshRequest, _ := json.Marshal(managementRequest{Method: http.MethodPost, Path: mgmtBase + "/refresh"})
	refreshCode, refreshBody := decodeMgmt(t, handleManagementHandle(refreshRequest))
	if refreshCode != http.StatusOK || refreshBody["refreshed"] != true || refreshBody["count"] != float64(1) || refreshBody["stale"] != false {
		t.Fatalf("POST /refresh after reconfigure failed: code=%d body=%#v", refreshCode, refreshBody)
	}
	if authFiles.Load() != 2 || apiCalls.Load() != 2 {
		t.Fatalf("POST /refresh did not perform fresh fill: auth-files=%d api-call=%d", authFiles.Load(), apiCalls.Load())
	}

	// 7. Test reconfigure followed by GET /accounts: must return full account detail
	if _, ok := HandleMethod("reconfigure", reconfigRequest); !ok {
		t.Fatal("reconfigure was not handled")
	}
	accountsRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: mgmtBase + "/accounts"})
	accountsCode, accountsBody := decodeMgmt(t, handleManagementHandle(accountsRequest))
	if accountsCode != http.StatusOK || accountsBody["configured"] != true || accountsBody["stale"] != false {
		t.Fatalf("GET /accounts after reconfigure failed: code=%d body=%#v", accountsCode, accountsBody)
	}
	accs, ok := accountsBody["accounts"].([]any)
	if !ok || len(accs) != 1 {
		t.Fatalf("GET /accounts accounts slice missing or wrong len: %#v", accountsBody["accounts"])
	}
}

// TestStatusRacesWithReconfigureGenerationCommit verifies that when an authenticated
// management status request is in flight and reconfigure intervenes, the collection
// result commits without generation contention, accounts are not dropped to 0,
// and health collections reflect the committed fill.
func TestStatusRacesWithReconfigureGenerationCommit(t *testing.T) {
	t.Setenv("QUOTA_MONITOR_DISABLE_BACKGROUND", "1")
	var authFiles atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/management/auth-files" {
			http.NotFound(w, r)
			return
		}
		if authFiles.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		_, _ = w.Write([]byte(`[{"provider":"codex","auth_index":"9","account":"race@example.invalid"}]`))
	}))
	defer server.Close()
	t.Setenv("QUOTA_MONITOR_MGMT_URL", server.URL)
	t.Setenv("QUOTA_MONITOR_MGMT_KEY", "fixture-management-key")
	configureFromEnv(nil)

	statusRequest, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: mgmtBase + "/status"})
	result := make(chan []byte, 1)
	go func() { result <- handleManagementHandle(statusRequest) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("status collection did not start")
	}

	reconfigRequest, _ := json.Marshal(struct {
		SchemaVersion uint32 `json:"schema_version"`
	}{SchemaVersion: 6})
	if _, ok := HandleMethod("reconfigure", reconfigRequest); !ok {
		t.Fatal("reconfigure was not handled")
	}
	close(release)

	select {
	case raw := <-result:
		code, body := decodeMgmt(t, raw)
		if code != http.StatusOK {
			t.Fatalf("racing status code=%d, want 200", code)
		}
		if body["configured"] != true {
			t.Fatalf("racing status configured=%v, want true", body["configured"])
		}
		if body["stale"] == true {
			t.Fatalf("racing status returned stale=true: %#v", body)
		}
		accCount, _ := body["accounts"].(float64)
		if accCount < 1 {
			t.Fatalf("racing status accounts=%v, want >= 1: %#v", body["accounts"], body)
		}
		health, ok := body["health"].(map[string]any)
		if !ok || health["collections"].(float64) < 1 {
			t.Fatalf("racing status health collections < 1: %#v", body["health"])
		}
	case <-time.After(managementCollectionTimeout):
		t.Fatal("racing status did not complete within bounded deadline")
	}
}
