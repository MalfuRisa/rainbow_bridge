package service

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestActiveRuntimeExportPinsPlanAcrossSwitchAndDelete(t *testing.T) {
	f := newReleaseFixture(t)
	first, err := f.create(t, "v1", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.create(t, "v2", "1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	paused, resume := make(chan struct{}), make(chan struct{})
	releaseExportPauseHookForTest = func(id string) {
		if id == first.Meta.SnapshotID {
			close(paused)
			<-resume
		}
	}
	t.Cleanup(func() { releaseExportPauseHookForTest = nil })
	type outcome struct {
		result *ReleaseStaticExportResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := f.svc.ExportActiveReleasePackage(adminTestContext(), "qa_prod", "main")
		done <- outcome{result, err}
	}()
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("export did not reach fixed plan")
	}
	_, switchErr := f.activate(t, "v2", "1", "")
	_, deleteErr := f.svc.DeleteRelease(adminTestContext(), ReleaseDeleteInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1",
		ExpectedSnapshotID: first.Meta.SnapshotID, RequestID: uuid.NewString(),
	})
	close(resume)
	got := <-done
	if got.result != nil {
		defer os.Remove(got.result.TempPath)
	}
	if switchErr != nil || deleteErr != nil || got.err != nil {
		t.Fatalf("switch/delete/export: %v / %v / %v", switchErr, deleteErr, got.err)
	}
	if got.result.SnapshotID != first.Meta.SnapshotID {
		t.Fatal("in-flight export changed snapshots")
	}
	bytes, err := os.ReadFile(got.result.TempPath)
	if err != nil {
		t.Fatal(err)
	}
	config := archiveJSON(t, bytes, "zip", "config.json")["data"].(map[string]any)
	if config["snapshot_id"] != first.Meta.SnapshotID || config["version"] != "v1" {
		t.Fatal("archive mixed snapshot metadata")
	}
	next, err := f.svc.ExportActiveReleasePackage(adminTestContext(), "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(next.TempPath)
	if next.SnapshotID != second.Meta.SnapshotID {
		t.Fatal("new request did not follow active")
	}
}

func TestActiveRuntimeExportTimeout(t *testing.T) {
	f := newReleaseFixture(t)
	if _, err := f.create(t, "v1", "1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(t, "v1", "0", ""); err != nil {
		t.Fatal(err)
	}
	previous := releaseExportTotalTimeout
	releaseExportTotalTimeout = time.Millisecond
	t.Cleanup(func() { releaseExportTotalTimeout = previous; releaseExportPauseHookForTest = nil })
	releaseExportPauseHookForTest = func(string) { time.Sleep(5 * time.Millisecond) }
	result, err := f.svc.ExportActiveReleasePackage(adminTestContext(), "qa_prod", "main")
	if result != nil {
		os.Remove(result.TempPath)
		t.Fatal("timed-out export returned a package")
	}
	requireReleaseErrorType(t, err, ErrTypeReleaseExportTimeout)
}
