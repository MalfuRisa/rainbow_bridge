// Remote test transport. Secrets stay in process memory or a private, disposable
// remote work directory; only sanitized topology and test results are printed.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "remote test transport:", err)
		os.Exit(1)
	}
}
func run() error {
	password := os.Getenv("RB_SSH_PASSWORD")
	if password == "" {
		return fmt.Errorf("SSH credential environment missing")
	}
	hostKey, err := knownhosts.New(os.Getenv("RB_SSH_KNOWN_HOSTS"))
	if err != nil {
		return fmt.Errorf("known-host file unavailable")
	}
	client, err := ssh.Dial("tcp", "10.60.226.43:22", &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.Password(password)}, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, HostKeyCallback: hostKey, Timeout: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer client.Close()
	command := func(command string, input []byte) ([]byte, error) {
		session, err := client.NewSession()
		if err != nil {
			return nil, err
		}
		defer session.Close()
		session.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		session.Stderr = &stderr
		output, err := session.Output(command)
		if err != nil {
			return output, fmt.Errorf("remote operation failed (details withheld)")
		}
		return output, nil
	}
	raw, err := command("kubectl --request-timeout=10s -n rainbow-bridge get configmap rainbow-bridge-config -o json", nil)
	if err != nil {
		return err
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(raw, &cm); err != nil {
		return err
	}
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		return fmt.Errorf("configuration decode failed")
	}
	pg, err := pgx.ParseConfig(cfg.Database.Postgres.DSN)
	if err != nil {
		return fmt.Errorf("database config parse failed")
	}
	if len(os.Args) < 2 || os.Args[1] == "inspect" {
		output, _ := json.Marshal(map[string]any{"driver": cfg.Database.Driver, "pgHost": pg.Host, "pgPort": pg.Port, "minioEndpoint": cfg.Storage.Minio.Endpoint, "minioRegion": cfg.Storage.Minio.Region, "redisAddress": cfg.Redis.Address})
		fmt.Println(string(output))
		owned, _ := command("kubectl --request-timeout=10s -n rainbow-bridge exec redis-0 -- sh -c 'for f in /tmp/rb-backend-*.pid; do test -f \"$f\" && printf \"%s \" \"$f\" && cat \"$f\" && tail -n 5 \"${f%.pid}.log\"; done; true'", nil)
		fmt.Print(string(owned))
		roots, err := command("find /var/tmp -maxdepth 1 -type d -name 'rb-backend-*' -print", nil)
		if err != nil {
			return fmt.Errorf("owned directory inspection failed")
		}
		fmt.Printf("Owned remote directories (empty means none):\n%s", roots)
		processes, err := command("for exe in /proc/[0-9]*/exe; do target=$(readlink \"$exe\" 2>/dev/null) || continue; case \"$target\" in /var/tmp/rb-backend-*/*) printf '%s %s\\n' \"$exe\" \"$target\";; esac; done", nil)
		if err != nil {
			return fmt.Errorf("owned process inspection failed")
		}
		fmt.Printf("Owned candidate processes (empty means none):\n%s", processes)
		return nil
	}
	if os.Args[1] == "cleanup-owned" && len(os.Args) == 3 && regexp.MustCompile(`^rb-backend-[0-9]{8}T[0-9]{6}$`).MatchString(os.Args[2]) {
		file := "/tmp/" + os.Args[2]
		_, err := command("kubectl --request-timeout=10s -n rainbow-bridge exec redis-0 -- sh -c 'if test -s "+file+".pid; then pid=$(cat "+file+".pid); test \"$(cat /proc/$pid/comm)\" = redis-server && kill -TERM \"$pid\"; sleep 1; fi; test ! -e "+file+".pid && rm -f -- "+file+".log'", nil)
		return err
	}
	if os.Args[1] != "test" && os.Args[1] != "inventory" {
		return fmt.Errorf("unsupported operation")
	}
	if pg.Host == "postgres" {
		host, err := command("kubectl --request-timeout=10s -n rainbow-bridge get svc postgres -o jsonpath='{.spec.externalName}'", nil)
		if err != nil {
			return err
		}
		mapped := strings.TrimSpace(string(host))
		if mapped == "" {
			return fmt.Errorf("PostgreSQL service target missing")
		}
		parts := strings.Split(mapped, ".")
		if len(parts) >= 3 && parts[2] == "svc" {
			address, err := command("kubectl --request-timeout=10s -n "+parts[1]+" get svc "+parts[0]+" -o jsonpath='{.spec.clusterIP}'", nil)
			if err != nil {
				return fmt.Errorf("PG service address unavailable")
			}
			mapped = strings.TrimSpace(string(address))
		}
		u := url.URL{Scheme: "postgres", User: url.UserPassword(pg.User, pg.Password), Host: net.JoinHostPort(mapped, fmt.Sprint(pg.Port)), Path: pg.Database}
		q := url.Values{}
		for key, value := range pg.RuntimeParams {
			q.Set(key, value)
		}
		if pg.TLSConfig == nil {
			q.Set("sslmode", "disable")
		} else {
			return fmt.Errorf("TLS PostgreSQL mapping requires explicit fixture configuration")
		}
		u.RawQuery = q.Encode()
		cfg.Database.Postgres.DSN = u.String()
	}
	// No changing directories or shared configuration. Root is unique and created
	// exclusively; never delete a caller-provided remote path.
	root := "/var/tmp/rb-backend-" + time.Now().UTC().Format("20060102T150405")
	transport := map[string]any{"root": root, "rootRemoved": false, "redisStopped": false, "forwardStopped": false}
	if _, err := command("umask 077; mkdir '"+root+"'", nil); err != nil {
		return err
	}
	defer func() {
		_, err := command("case '"+root+"' in /var/tmp/rb-backend-*) rm -rf -- '"+root+"';; *) exit 1;; esac", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cleanup pending for owned directory:", root)
		}
		transport["rootRemoved"] = err == nil
		if path := os.Getenv("RB_REMOTE_TEST_LOG"); path != "" {
			b, _ := json.MarshalIndent(transport, "", "  ")
			os.WriteFile(path+".transport.json", b, 0600)
		}
	}()
	// The live instance gets read-only inventory only, never schema migration.
	if os.Args[1] == "inventory" {
		encoded, err := yaml.Marshal(&cfg)
		if err != nil {
			return err
		}
		if _, err := command("umask 077; cat > '"+root+"/inventory.yaml'", encoded); err != nil {
			return err
		}
		binary, err := os.ReadFile(os.Getenv("RB_REMOTE_MAINTENANCE_BINARY"))
		if err != nil {
			return fmt.Errorf("maintenance binary missing")
		}
		if _, err := command("cat > '"+root+"/release-storage'; chmod 700 '"+root+"/release-storage'", binary); err != nil {
			return err
		}
		output, err := command("cd '"+root+"' && ./release-storage --config inventory.yaml --action plan", nil)
		if err == nil {
			listing, listErr := command("kubectl --request-timeout=10s -n rainbow-bridge exec deployment/rainbow-bridge-app -- sh -c 'if test -d /app/data/release-snapshots; then find /app/data/release-snapshots -maxdepth 2 -mindepth 1 -print; else printf \"LIVE_LOCAL_ROOT_ABSENT\\n\"; fi'", nil)
			if listErr != nil {
				return fmt.Errorf("live local snapshot directory inspection unavailable")
			}
			output = append(output, listing...)
		}
		if path := os.Getenv("RB_REMOTE_TEST_LOG"); path != "" {
			os.WriteFile(path, output, 0600)
		}
		fmt.Print(string(output))
		return err
	}
	// A second Redis process, loopback-only, with no persistence. The existing
	// Redis server and its database are never configured, stopped or flushed.
	port := 22000 + time.Now().Nanosecond()%10000
	transport["redisPort"] = port
	pidPath := "/tmp/" + filepath.Base(root) + ".pid"
	logPath := "/tmp/" + filepath.Base(root) + ".log"
	defer func() {
		_, err := command(fmt.Sprintf("kubectl --request-timeout=10s -n rainbow-bridge exec redis-0 -- sh -c 'if test -s %s; then redis-cli -p %d shutdown nosave || exit 1; fi; test ! -e %s || exit 1; rm -f -- %s; case %s in /tmp/rb-backend-*) rm -rf -- %s;; *) exit 1;; esac'", pidPath, port, pidPath, logPath, "/tmp/"+filepath.Base(root), "/tmp/"+filepath.Base(root)), nil)
		transport["redisStopped"] = err == nil
		if err != nil {
			fmt.Fprintln(os.Stderr, "owned Redis cleanup needs review")
		}
	}()
	start := fmt.Sprintf("kubectl --request-timeout=10s -n rainbow-bridge exec redis-0 -- sh -c 'test ! -e %s && mkdir /tmp/%s && redis-server --port %d --bind 127.0.0.1 --dir /tmp/%s --save \"\" --appendonly no --daemonize yes --pidfile %s --logfile %s && sleep 1 && test -s %s'", pidPath, filepath.Base(root), port, filepath.Base(root), pidPath, logPath, pidPath)
	if _, err := command(start, nil); err != nil {
		return fmt.Errorf("isolated Redis could not be started; owned-process cleanup attempted")
	}
	forward := fmt.Sprintf("kubectl -n rainbow-bridge port-forward --address 127.0.0.1 pod/redis-0 %d:%d > '%s/redis-forward.log' 2>&1 & echo $! > '%s/redis-forward.pid'", port, port, root, root)
	if _, err := command(forward, nil); err != nil {
		return err
	}
	defer func() {
		_, err := command("kill $(cat '"+root+"/redis-forward.pid') 2>/dev/null || true", nil)
		transport["forwardStopped"] = err == nil
	}()
	cfg.Redis.Address = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.Redis.DB = 0
	cfg.Redis.Password = ""
	cfg.Redis.Enabled = true
	encoded, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	if _, err := command("umask 077; cat > '"+root+"/source.yaml'", encoded); err != nil {
		return err
	}
	binary, err := os.ReadFile(os.Getenv("RB_REMOTE_TEST_BINARY"))
	if err != nil {
		return fmt.Errorf("test binary missing")
	}
	if _, err := command("cat > '"+root+"/service.test'; chmod 700 '"+root+"/service.test'", binary); err != nil {
		return err
	}
	candidate, err := os.ReadFile(os.Getenv("RB_REMOTE_CANDIDATE_BINARY"))
	if err != nil {
		return fmt.Errorf("candidate binary missing")
	}
	if _, err := command("cat > '"+root+"/hertz_service'; chmod 700 '"+root+"/hertz_service'", candidate); err != nil {
		return err
	}
	maintenance, err := os.ReadFile(os.Getenv("RB_REMOTE_MAINTENANCE_BINARY"))
	if err != nil {
		return fmt.Errorf("maintenance binary missing")
	}
	if _, err := command("cat > '"+root+"/release-storage'; chmod 700 '"+root+"/release-storage'", maintenance); err != nil {
		return err
	}
	output, err := command("mkdir '"+root+"/tmp' && cd '"+root+"' && TMPDIR='"+root+"/tmp' RB_REMOTE_MAINTENANCE_BINARY='"+root+"/release-storage' RB_REMOTE_CANDIDATE_BINARY='"+root+"/hertz_service' RB_REMOTE_COMPONENT_CONFIG='"+root+"/source.yaml' ./service.test -test.run '^TestBackendRemoteComponents$' -test.v -test.timeout 300s", nil)
	cleanup, cleanupErr := command("cat '"+root+"/component-cleanup.json'", nil)
	if cleanupErr == nil && os.Getenv("RB_REMOTE_TEST_LOG") != "" {
		os.WriteFile(os.Getenv("RB_REMOTE_TEST_LOG")+".cleanup.json", cleanup, 0600)
	}
	if path := os.Getenv("RB_REMOTE_TEST_LOG"); path != "" {
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, output, 0600)
	}
	fmt.Print(string(output))
	return err
}
