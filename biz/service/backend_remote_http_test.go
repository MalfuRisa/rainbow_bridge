package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	appconfig "github.com/yi-nology/rainbow_bridge/pkg/config"
	"gopkg.in/yaml.v3"
)

// Run the freshly built real backend, not a mock HTTP facade, on loopback only.
func verifyRemoteCandidateHTTP(t *testing.T, cfg appconfig.Config, schema, work string) {
	t.Helper()
	binary := os.Getenv("RB_REMOTE_CANDIDATE_BINARY")
	if binary == "" {
		t.Fatal("owned candidate binary required for HTTP verification")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	base := "http://" + address + "/rainbow-bridge/"
	u, err := url.Parse(cfg.Database.Postgres.DSN)
	if err != nil {
		t.Fatal("fixture DSN parse failed")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	cfg.Database.Postgres.DSN = u.String()
	cfg.Server.Address = address
	cfg.Server.BasePath = "/rainbow-bridge"
	cfg.Backup.Enabled = false
	cfg.Log.Output = "stdout"
	cfg.Log.Level = "error"
	insecure := false
	cfg.Auth = appconfig.AuthConfig{CookieName: "rb_owned_candidate", CookieSecure: &insecure, AllowInsecureLocal: true, TrustedOrigins: []string{"http://" + address}}
	appconfig.ApplyAuthDefaults(&cfg)
	encoded, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(work, "candidate.yaml")
	if err := os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(configPath)
	password := "Qa-owned-" + uuid.NewString() + "!9"
	var process *exec.Cmd
	var log *os.File
	instanceNumber := 0
	stop := func() {
		if process != nil {
			process.Process.Signal(syscall.SIGTERM)
			done := make(chan struct{})
			go func() { process.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				process.Process.Kill()
				<-done
			}
			process = nil
		}
		if log != nil {
			log.Close()
			log = nil
		}
	}
	defer stop()
	start := func() {
		t.Helper()
		instanceNumber++
		log, err = os.OpenFile(filepath.Join(work, "candidate.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		process = exec.Command(binary, "--config", configPath, "--no-frontend")
		process.Dir = filepath.Join(work, fmt.Sprintf("candidate-instance-%d", instanceNumber))
		if err := os.Mkdir(process.Dir, 0700); err != nil {
			t.Fatal(err)
		}
		process.Stdout = log
		process.Stderr = log
		process.Env = append(os.Environ(), "RAINBOW_ADMIN_USERNAME=qa_candidate", "RAINBOW_ADMIN_INITIAL_PASSWORD="+password, "RAINBOW_RELEASE_DIR=unavailable-permanent-directory")
		if err := process.Start(); err != nil {
			t.Fatal("owned candidate start failed")
		}
		for i := 0; i < 80; i++ {
			r, err := http.Get(base + "ping")
			if err == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("owned candidate did not become ready; sanitized startup diagnostics required")
	}
	start()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	public := &http.Client{Timeout: 15 * time.Second}
	csrf := ""
	selectedEnvironment := ""
	call := func(method, endpoint string, payload any, anonymous bool, want int) (map[string]any, []byte) {
		t.Helper()
		var body io.Reader
		if payload != nil {
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(b)
		}
		r, err := http.NewRequest(method, base+"api/v1/"+endpoint, body)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Origin", "http://"+address)
		if strings.HasPrefix(endpoint, "runtime/") {
			r.Header.Set("x-environment", selectedEnvironment)
			r.Header.Set("x-pipeline", "default")
		}
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if csrf != "" && !anonymous {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		selected := client
		if anonymous {
			selected = public
		}
		response, err := selected.Do(r)
		if err != nil {
			t.Fatalf("HTTP %s unavailable", endpoint)
		}
		defer response.Body.Close()
		b, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("HTTP %s status=%d wanted=%d", endpoint, response.StatusCode, want)
		}
		var envelope map[string]any
		json.Unmarshal(b, &envelope)
		if want == 200 && envelope != nil {
			code, _ := envelope["code"].(float64)
			if code != 0 && code != 200 {
				t.Fatalf("business failure endpoint=%s code=%v type=%v", endpoint, code, envelope["error_type"])
			}
		}
		data, _ := envelope["data"].(map[string]any)
		return data, b
	}
	login := func() {
		data, _ := call("POST", "auth/login", map[string]any{"username": "qa_candidate", "password": password}, false, 200)
		csrf = data["csrf_token"].(string)
	}
	login()
	env := "http_" + uuid.NewString()[:8]
	selectedEnvironment = env
	data, _ := call("POST", "environment/create", map[string]any{"environment": map[string]any{"environment_key": env, "environment_name": env, "is_active": true}}, false, 200)
	_ = data
	data, _ = call("GET", "pipeline/list?environment_key="+env, nil, false, 200)
	scope := data["list"].([]any)[0].(map[string]any)["scope_id"].(string)
	identity := map[string]any{"environment_key": env, "pipeline_key": "default", "expected_scope_id": scope}
	query := "environment_key=" + env + "&pipeline_key=default&scope_id=" + scope
	data, _ = call("GET", "config/list?"+query, nil, false, 200)
	if count, _ := data["total"].(float64); count != 0 {
		t.Fatal("new HTTP scope not empty")
	}
	// Upload actual source bytes through the registered multipart handler.
	payload := []byte("HTTP immutable MinIO source")
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	for key, value := range map[string]string{"environment_key": env, "pipeline_key": "default", "expected_scope_id": scope} {
		form.WriteField(key, value)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="owned.txt"`)
	header.Set("Content-Type", "text/plain")
	file, _ := form.CreatePart(header)
	file.Write(payload)
	form.Close()
	req, _ := http.NewRequest("POST", base+"api/v1/asset/upload", &upload)
	req.Header.Set("Origin", "http://"+address)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("HTTP upload unavailable")
	}
	var uploaded map[string]any
	json.NewDecoder(response.Body).Decode(&uploaded)
	response.Body.Close()
	if response.StatusCode != 200 || uploaded["code"] != float64(200) {
		t.Fatal("HTTP owned upload failed")
	}
	asset := uploaded["data"].(map[string]any)["asset"].(map[string]any)
	fileID := asset["file_id"].(string)
	configs := []map[string]any{{"alias": "FILE", "name": "file", "type": "file", "content": "asset://" + fileID}, {"alias": "BIG", "name": "big", "type": "object", "content": `{"big":900719925474099312345}`}}
	for _, config := range configs {
		config["environment_key"] = env
		config["pipeline_key"] = "default"
		call("POST", "config/create", map[string]any{"expected_scope_id": scope, "config": config}, false, 200)
	}
	data, _ = call("GET", "config/list?"+query, nil, false, 200)
	if data["total"] != float64(2) {
		t.Fatal("real HTTP cached empty list not invalidated")
	}
	lock := map[string]any{}
	for k, v := range identity {
		lock[k] = v
	}
	lock["expected_revision"] = "0"
	data, _ = call("POST", "freeze/lock", lock, false, 200)
	revision := data["revision"].(string)
	create := map[string]any{}
	for k, v := range identity {
		create[k] = v
	}
	create["expected_freeze_revision"] = revision
	create["request_id"] = uuid.NewString()
	data, _ = call("POST", "release/create", create, false, 200)
	meta := data["meta"].(map[string]any)
	snapshot := meta["snapshot_id"].(string)
	version := meta["version"].(string)
	replayed, _ := call("POST", "release/create", create, false, 200)
	replayedMeta, ok := replayed["meta"].(map[string]any)
	if !ok || replayedMeta["snapshot_id"] != snapshot || replayedMeta["version"] != version {
		t.Fatal("HTTP create replay returned a different snapshot/version")
	}
	// An independent process with a different empty working directory reads,
	// compares and activates the version created by the first process.
	stop()
	start()
	comparison := map[string]any{}
	for k, v := range identity {
		comparison[k] = v
	}
	comparison["version"] = version
	comparison["expected_snapshot_id"] = snapshot
	comparison["expected_active_revision"] = "0"
	data, _ = call("POST", "release/compare", comparison, false, 200)
	if data["has_changes"] != true {
		t.Fatal("HTTP comparison did not show first publication")
	}
	activate := map[string]any{}
	for k, v := range identity {
		activate[k] = v
	}
	activate["version"] = version
	activate["expected_active_revision"] = "0"
	activate["request_id"] = uuid.NewString()
	call("POST", "release/activate", activate, false, 200)
	readRuntime := func() {
		t.Helper()
		req, _ := http.NewRequest("GET", base+"api/v1/runtime/release?version="+version+"&scope_id="+scope+"&content_format=raw", nil)
		req.Header.Set("x-environment", env)
		req.Header.Set("x-pipeline", "default")
		response, err := public.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if response.StatusCode != 200 || !bytes.Contains(body, []byte("900719925474099312345")) {
			t.Fatal("anonymous fixed runtime lost content")
		}
		var result struct {
			Data struct {
				Configs []struct{ Alias, Content string }
			}
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		fileCount := 0
		for _, config := range result.Data.Configs {
			if config.Alias != "FILE" {
				continue
			}
			fileCount++
			u, err := url.Parse(config.Content)
			if err != nil {
				t.Fatal(err)
			}
			target := config.Content
			if !u.IsAbs() {
				target = "http://" + address + config.Content
			}
			r, err := public.Get(target)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			r.Body.Close()
			if r.StatusCode != 200 || !bytes.Equal(b, payload) {
				t.Fatal("anonymous release resource bytes changed")
			}
		}
		if fileCount != 1 {
			t.Fatalf("anonymous fixed runtime expected exactly one FILE and download, got %d", fileCount)
		}
	}
	readRuntime()
	for _, endpoint := range []string{"runtime/static?" + query, "runtime/release/static?" + query + "&version=" + version + "&expected_snapshot_id=" + snapshot} {
		_, body := call("GET", endpoint, nil, true, 200)
		z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil || len(z.File) < 3 {
			t.Fatal("real HTTP static ZIP incomplete")
		}
	}
	call("GET", "release/detail?"+query+"&version="+version, nil, false, 200)
	call("GET", "backup/capabilities", nil, false, 200)
	// Restart only this candidate child, keeping the same owned PG schema and directory.
	stop()
	start()
	readRuntime()
	login()
	call("POST", "auth/logout", map[string]any{}, false, 200)
	t.Log("CANDIDATE HTTP: real login, cache readback, MinIO multipart, freeze/create/replay/compare/activate, anonymous runtime/resource/ZIP and isolated process restart passed")
}
