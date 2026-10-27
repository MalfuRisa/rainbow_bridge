package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/backup"
	appconfig "github.com/yi-nology/rainbow_bridge/pkg/config"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the same config.yaml used by the service")
	command := flag.String("command", "", "validate | stage | status | cancel | rollback | cleanup")
	packagePath := flag.String("package", "", "backup ZIP path (validate/stage)")
	jobID := flag.String("job", "", "restore job id (cancel)")
	operationID := flag.String("operation", "", "restore operation id (cleanup)")
	confirm := flag.String("confirm", "", "explicit phrase (cleanup)")
	requestID := flag.String("request-id", "", "idempotency UUID (stage)")
	reason := flag.String("reason", "", "operator reason (stage/rollback)")
	flag.Parse()

	if *command == "" {
		usage()
		os.Exit(2)
	}
	cfg, err := appconfig.Load(*configPath)
	if err != nil {
		fail("load config: %v", err)
	}
	manager, closeLock, err := backup.OpenOffline(cfg, backup.Producer{
		AppVersion: "cli", GitCommit: "cli",
	})
	if err != nil {
		fail("open control directory: %v", err)
	}
	defer closeLock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	switch *command {
	case "validate":
		runValidate(ctx, manager, *packagePath)
	case "stage":
		runStage(ctx, manager, *packagePath, *requestID, *reason)
	case "status":
		runStatus(manager)
	case "cancel":
		runCancel(ctx, manager, *jobID)
	case "rollback":
		runRollback(ctx, manager, *reason)
	case "cleanup":
		runCleanup(manager, *operationID, *confirm)
	default:
		usage()
		os.Exit(2)
	}
}

func runValidate(ctx context.Context, manager *backup.Manager, packagePath string) {
	absolute, err := requirePackage(packagePath)
	if err != nil {
		fail("%v", err)
	}
	info, issues, err := manager.ValidateFile(ctx, absolute)
	if err != nil {
		fail("validate: %v", err)
	}
	payload := map[string]any{
		"valid":          len(issues) == 0,
		"package":        absolute,
		"package_sha256": info.PackageSHA256,
		"package_size":   info.PackageSize,
		"manifest":       info.Manifest,
		"issues":         append([]string{}, issues...),
	}
	printJSON(payload)
	if len(issues) > 0 {
		os.Exit(1)
	}
}

func runStage(ctx context.Context, manager *backup.Manager, packagePath, requestID, reason string) {
	absolute, err := requirePackage(packagePath)
	if err != nil {
		fail("%v", err)
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	job, err := manager.StagePackage(ctx, "offline-operator", requestID, absolute, reason)
	if err != nil {
		fail("stage: %v", err)
	}
	printJSON(job)
	if job.State != backup.StateRestartRequired {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "candidate prepared; restart the service to install it")
}

func runStatus(manager *backup.Manager) {
	status, err := backup.InspectManager(manager)
	if err != nil {
		fail("status: %v", err)
	}
	printJSON(status)
}

func runCancel(ctx context.Context, manager *backup.Manager, jobID string) {
	job, err := manager.CancelPrepared(ctx, jobID)
	if err != nil {
		fail("cancel: %v", err)
	}
	printJSON(job)
}

func runRollback(ctx context.Context, manager *backup.Manager, reason string) {
	job, err := manager.OfflineRollback(ctx, "offline-operator", reason)
	if err != nil {
		fail("rollback: %v", err)
	}
	printJSON(job)
}

func runCleanup(manager *backup.Manager, operationID, confirmation string) {
	if err := manager.CleanupRollback(operationID, confirmation, "offline-operator"); err != nil {
		fail("cleanup: %v", err)
	}
	printJSON(map[string]any{"deleted": true, "operation_id": operationID})
}

func requirePackage(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("-package is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("package file not found")
	}
	return absolute, nil
}

func printJSON(value any) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fail("encode output: %v", err)
	}
	fmt.Println(string(payload))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  backup-restore -config config.yaml -command validate -package backup.zip
  backup-restore -config config.yaml -command stage -package backup.zip [-request-id UUID] [-reason TEXT]
  backup-restore -config config.yaml -command status
  backup-restore -config config.yaml -command cancel [-job JOB_ID]
  backup-restore -config config.yaml -command rollback [-reason TEXT]
  backup-restore -config config.yaml -command cleanup -operation OP_ID -confirm DELETE_ROLLBACK_COPY

Run only while the service is stopped. The tool takes the same instance lock
and refuses to operate when another process holds it.
`)
}
