package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yi-nology/rainbow_bridge/biz/dal/model"
	"github.com/yi-nology/rainbow_bridge/pkg/config"
)

// --------------------- F1:富文本媒体引用边界 ---------------------

// TestReleaseRichTextMediaRejections 复核 F1:五种漏检输入与相对路径媒体都必须在捕获阶段明确拒绝。
func TestReleaseRichTextMediaRejections(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		expected string
	}{
		{name: "unquoted_external_src", content: `<img src=https://cdn.example.com/a.png>`, expected: "external_reference"},
		{name: "external_srcset", content: `<img srcset="https://cdn.example.com/a.png 1x, https://cdn.example.com/b.png 2x">`, expected: "external_reference"},
		{name: "external_poster", content: `<video poster="https://cdn.example.com/poster.png"></video>`, expected: "external_reference"},
		{name: "protocol_relative_src", content: `<img src="//cdn.example.com/a.png">`, expected: "external_reference"},
		{name: "style_background_url", content: `<div style="background-image:url(https://cdn.example.com/a.png)"></div>`, expected: "external_reference"},
		{name: "style_block_import", content: `<style>@import "https://cdn.example.com/x.css";</style>`, expected: "external_reference"},
		{name: "relative_src", content: `<img src="/uploads/a.png">`, expected: "unresolvable_reference"},
		{name: "relative_srcset_candidate", content: `<img srcset="pics/a.png 1x">`, expected: "unresolvable_reference"},
		{name: "unterminated_quote_media", content: `<img src="https://cdn.example.com/a.png>`, expected: "external_reference"},
		// 复核补充一:引号内的 '>' 是合法字符,不得让扫描器提前结束并漏掉后面的媒体属性
		{name: "quoted_gt_before_external_src", content: `<img alt=">" src="https://cdn.example.com/a.png">`, expected: "external_reference"},
		{name: "quoted_gt_text_before_external_src", content: `<img alt="a > b" src="https://cdn.example.com/a.png">`, expected: "external_reference"},
		{name: "quoted_gt_in_style_media", content: `<div title="a > b" style="background-image:url(https://cdn.example.com/a.png)"></div>`, expected: "external_reference"},
		// 复核补充二:srcset 第一个候选不带描述符时,结尾逗号就是候选分隔符,不得吞掉后续候选
		{name: "srcset_fragment_first_then_external", content: `<img srcset="#local, https://cdn.example.com/a.png 2x">`, expected: "external_reference"},
		{name: "srcset_two_candidates_no_descriptor_then_external", content: `<img srcset="#a, #b, https://cdn.example.com/a.png 2x">`, expected: "external_reference"},
		// 复核补充三:CSS 字符串里的 ')' 是合法字符,不得据此提前结束 url() 值
		{name: "css_url_quoted_relative_with_paren", content: `<div style="background-image:url(')banner.png')"></div>`, expected: "unresolvable_reference"},
		{name: "css_url_quoted_external_with_paren", content: `<div style='background-image:url("https://cdn.example.com/a.png?x=1)")'></div>`, expected: "external_reference"},
		{name: "css_url_quoted_protocol_relative_with_paren", content: `<div style="background:url('//cdn.example.com/a.png)x')"></div>`, expected: "external_reference"},
		{name: "style_block_quoted_relative_with_paren", content: `<style>.a{background:url(")b.png")}</style>`, expected: "unresolvable_reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFreezeService(t)
			seedFreezeScope(t, svc, "qa_prod", "main")
			ctx := adminTestContext()
			if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "RICH_MEDIA", "richtext", tc.content)); err != nil {
				t.Fatal(err)
			}
			scopeRow, err := svc.logic.environmentDAO.GetByKey(ctx, svc.logic.db, "qa_prod")
			if err != nil {
				t.Fatal(err)
			}
			pipelineRow, err := svc.logic.pipelineDAO.GetByKey(ctx, svc.logic.db, "qa_prod", "main")
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.captureReleasePlan(ctx, svc.logic, "qa_prod", "main", scopeRow, pipelineRow)
			releaseErr := requireReleaseErrorType(t, err, ErrTypeReleaseResourceInvalid)
			found := false
			for _, issue := range releaseErr.Issues {
				if issue.Reason == tc.expected {
					found = true
				}
			}
			if !found {
				t.Fatalf("want issue %s, got %+v", tc.expected, releaseErr.Issues)
			}
			versions, _, err := svc.logic.releaseDAO.ListVersions(ctx, svc.logic.db, "qa_prod", "main", 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(versions) != 0 {
				t.Fatalf("rejected capture must not leave a version, got %d", len(versions))
			}
		})
	}
}

// TestReleaseRichTextBusinessHyperlinkStaysLiteral 普通业务超链接保留原值,只登记为外部提示,不阻断创建。
func TestReleaseRichTextBusinessHyperlinkStaysLiteral(t *testing.T) {
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()
	content := `<p><a href="https://docs.example.com/guide?v=2#top">文档</a>与<a href="/relative/page">站内页</a></p>`
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "RICH_LINK", "richtext", content)); err != nil {
		t.Fatal(err)
	}
	scopeRow, err := svc.logic.environmentDAO.GetByKey(ctx, svc.logic.db, "qa_prod")
	if err != nil {
		t.Fatal(err)
	}
	pipelineRow, err := svc.logic.pipelineDAO.GetByKey(ctx, svc.logic.db, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.captureReleasePlan(ctx, svc.logic, "qa_prod", "main", scopeRow, pipelineRow)
	if err != nil {
		t.Fatalf("business hyperlinks must not block creation: %v", err)
	}
	if !strings.Contains(plan.Payload, "https://docs.example.com/guide?v=2#top") {
		t.Fatalf("hyperlink value must stay literal: %s", plan.Payload)
	}
	if len(plan.ExternalReferences) != 1 || plan.ExternalReferences[0] != "https://docs.example.com/guide?v=2#top" {
		t.Fatalf("external hyperlink must be listed as hint, got %+v", plan.ExternalReferences)
	}
}

// TestReleaseRichTextManagedMediaRoundTrip 受管媒体在全部位置(引号/无引号、srcset、poster、style、链接)捕获与渲染一致。
func TestReleaseRichTextManagedMediaRoundTrip(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "qa_prod", "main")
	ctx := adminTestContext()

	fileA, fileB := "rich-media-a", "rich-media-b"
	writeUploadedFile(t, fileA, "a.png", []byte("A-BYTES"))
	writeUploadedFile(t, fileB, "b.png", []byte("B-BYTES"))
	for _, asset := range []*model.Asset{
		{FileID: fileA, EnvironmentKey: "qa_prod", PipelineKey: "main", FileName: "a.png", ContentType: "image/png", FileSize: int64(len("A-BYTES")), Path: filepath.Join(uploadDirectory, fileA, "a.png")},
		{FileID: fileB, EnvironmentKey: "qa_prod", PipelineKey: "main", FileName: "b.png", ContentType: "image/png", FileSize: int64(len("B-BYTES")), Path: filepath.Join(uploadDirectory, fileB, "b.png")},
	} {
		if err := svc.logic.CreateAsset(ctx, asset); err != nil {
			t.Fatal(err)
		}
	}

	content := strings.Join([]string{
		`<p>图文内容</p>`,
		`<img src="asset://` + fileA + `"/>`,
		`<img srcset="asset://` + fileA + ` 1x, asset://` + fileB + ` 2x">`,
		`<video poster='asset://` + fileA + `'></video>`,
		`<div style="background-image:url(asset://` + fileB + `)"></div>`,
		`<a href="asset://` + fileA + `?v=1#top">下载</a>`,
		`<img src="/rainbow-bridge/api/v1/asset/file/` + fileB + `/b.png">`,
		`<img src=asset://` + fileA + `>`,
		`<a href="https://example.com/docs">说明</a>`,
		// 复核补充:引号内的大于号不得影响后续属性;srcset 首项不带描述符时两个受管候选都要重写;
		// CSS 字符串里的 ')' 不得截断 url() 的值(含后缀里带 ')' 的受管引用)
		`<img alt="a > b" src="asset://` + fileA + `">`,
		`<img srcset="asset://` + fileA + `, asset://` + fileB + ` 2x">`,
		`<div style="background-image:url('asset://` + fileA + `?v=1#top')"></div>`,
		`<style>.c{background:url("asset://` + fileB + `?x=a)b")}</style>`,
	}, "")
	if _, err := svc.AddConfig(ctx, buildConfig("qa_prod", "main", "RICH_MEDIA", "richtext", content)); err != nil {
		t.Fatal(err)
	}
	mustLock(t, svc, "qa_prod", "main", "富文本媒体往返")

	result, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "富文本媒体往返",
	})
	if err != nil {
		t.Fatalf("create with managed media: %v", err)
	}
	if len(result.Meta.ExternalReferences) != 1 || result.Meta.ExternalReferences[0] != "https://example.com/docs" {
		t.Fatalf("only business hyperlink may be listed as external: %+v", result.Meta.ExternalReferences)
	}

	row, err := svc.logic.releaseDAO.GetVersionByScopeAndName(ctx, svc.logic.db, "qa_prod", "main", "v1.0")
	if err != nil {
		t.Fatal(err)
	}
	if containsBareAssetScheme(row.Payload) || strings.Contains(row.Payload, fileURLPrefix) {
		t.Fatalf("payload must keep only markers for managed media: %s", row.Payload)
	}
	if !strings.Contains(row.Payload, "https://example.com/docs") {
		t.Fatalf("business hyperlink must stay literal in payload: %s", row.Payload)
	}
	if got := strings.Count(row.Payload, releaseAssetScheme); got != 13 {
		t.Fatalf("expected 13 managed markers in payload, got %d: %s", got, row.Payload)
	}

	runtimeData, err := svc.RuntimeRelease(ctx, "qa_prod", "main", strPtr("v1.0"))
	if err != nil {
		t.Fatal(err)
	}
	rendered := findRuntimeConfig(t, runtimeData, "RICH_MEDIA")
	prefix := "/rainbow-bridge/api/v1/release/asset/" + result.Meta.SnapshotID + "/"
	for _, want := range []string{
		`src="` + prefix,
		`src=` + prefix, // 无引号形式保持无引号
		`alt="a > b"`,   // 引号内的大于号原样保留
		`'` + prefix,    // poster 单引号
		"url(" + prefix,
		`href="` + prefix,
		"?v=1#top",
		" 1x, ",
		" 2x",
	} {
		if !strings.Contains(rendered.Content, want) {
			t.Fatalf("rendered richtext missing %q: %s", want, rendered.Content)
		}
	}
	if got := strings.Count(rendered.Content, prefix); got != 13 {
		t.Fatalf("expected 13 rendered references, got %d: %s", got, rendered.Content)
	}
	if strings.Contains(rendered.Content, "/api/v1/asset/file/") {
		t.Fatalf("editor asset routes must not leak into release runtime: %s", rendered.Content)
	}
	if !strings.Contains(rendered.Content, `href="https://example.com/docs"`) {
		t.Fatalf("business hyperlink must stay as-is: %s", rendered.Content)
	}
}

// TestReleaseRichTextScanBoundaries 扫描器本身的边界:注释/结束标签/脚本体不应误判,闭合缺失不越界。
func TestReleaseRichTextScanBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{name: "closing_tag_skipped", content: `</img><img src="https://a.example.com/x.png">`, want: 1},
		{name: "comment_skipped", content: `<!-- <img src="https://a.example.com/x.png"> -->`, want: 0},
		{name: "math_text", content: `1 < 2 <img src="https://a.example.com/x.png">`, want: 1},
		{name: "style_text_only", content: `<style>.a{color:red}</style>`, want: 0},
		{name: "identifier_not_url", content: `<div style="x-url(y)"></div>`, want: 0},
		{name: "link_href_is_media", content: `<link href="https://cdn.example.com/x.css">`, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(scanReleaseHTMLReferences(tc.content)); got != tc.want {
				t.Fatalf("scan %q: want %d references, got %d (%+v)", tc.content, tc.want, got, scanReleaseHTMLReferences(tc.content))
			}
		})
	}
}

// --------------------- F2:请求指纹字段边界 ---------------------

// TestReleaseFingerprintFieldBoundariesDoNotCollide 复核 F2:跨字段边界的两个范围不能被当成同一请求。
func TestReleaseFingerprintFieldBoundariesDoNotCollide(t *testing.T) {
	left := releaseFingerprint("create", "a|b", "c", "v1.0", "1", "reason")
	right := releaseFingerprint("create", "a", "b|c", "v1.0", "1", "reason")
	if left == right {
		t.Fatal("fingerprint must not collide across field boundaries")
	}
	if releaseFingerprint("create", "a", "b", "v1.0", "1", "reason") != releaseFingerprint("create", "a", "b", "v1.0", "1", "reason") {
		t.Fatal("identical fields must produce identical fingerprint")
	}
	if releaseFingerprint("activate", "a", "b") == releaseFingerprint("create", "a", "b") {
		t.Fatal("operation must be part of the fingerprint")
	}
}

// TestReleaseCrossScopeRequestIDIsConflict 端到端:a|b/c 与 a/b|c 共用 request_id 必须 409,真正重放仍可用。
func TestReleaseCrossScopeRequestIDIsConflict(t *testing.T) {
	t.Setenv(FreezeAdminTokenEnv, testAdminToken())
	svc := newFreezeService(t)
	seedFreezeScope(t, svc, "a|b", "c")
	seedFreezeScope(t, svc, "a", "b|c")
	ctx := adminTestContext()
	for _, pair := range [][2]string{{"a|b", "c"}, {"a", "b|c"}} {
		if _, err := svc.AddConfig(ctx, buildConfig(pair[0], pair[1], "SCOPE_TEXT", "text", "scope="+pair[0]+"|"+pair[1])); err != nil {
			t.Fatal(err)
		}
		mustLock(t, svc, pair[0], pair[1], "指纹边界锁定")
	}

	requestID := uuid.NewString()
	reason := "指纹边界"
	first, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "a|b", PipelineKey: "c", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: requestID, Reason: reason,
	})
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.Meta.EnvironmentKey != "a|b" || first.Meta.PipelineKey != "c" {
		t.Fatalf("first create scope mismatch: %+v", first.Meta)
	}

	second, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "a", PipelineKey: "b|c", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: requestID, Reason: reason,
	})
	requireReleaseErrorType(t, err, ErrTypeReleaseRequestConflict)
	if second != nil {
		t.Fatalf("conflicting request must not return another scope's result: %+v", second.Meta)
	}
	versions, _, err := svc.logic.releaseDAO.ListVersions(ctx, svc.logic.db, "a", "b|c", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("conflicting scope must not get a version, got %d", len(versions))
	}

	replay, err := svc.CreateRelease(ctx, ReleaseCreateInput{
		EnvironmentKey: "a|b", PipelineKey: "c", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: requestID, Reason: reason,
	})
	if err != nil {
		t.Fatalf("true replay must succeed: %v", err)
	}
	if !replay.Replayed || replay.Meta.SnapshotID != first.Meta.SnapshotID {
		t.Fatalf("true replay must return the original snapshot: %+v", replay)
	}
}

// --------------------- R09/R17 时序正式回归 ---------------------

// TestReleaseActivateReplayAfterFurtherSwitches R09:激活响应丢失后,期间又切到别的版本,
// 重放原切换请求必须返回原 applied_revision 与最新 active,且不把状态切回去。
func TestReleaseActivateReplayAfterFurtherSwitches(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	if _, err := fixture.create(t, "v1.0", "1", ""); err != nil {
		t.Fatalf("create v1.0: %v", err)
	}
	if _, err := fixture.create(t, "v1.1", "1", ""); err != nil {
		t.Fatalf("create v1.1: %v", err)
	}
	activate := func(version, expected string, id string) (*ReleaseActivateResult, error) {
		return svc.ActivateRelease(ctx, ReleaseActivateInput{
			EnvironmentKey: "qa_prod", PipelineKey: "main", Version: version,
			ExpectedActiveRevisionRaw: expected, RequestID: id, Reason: "时序回归 " + version,
		})
	}
	requestID := uuid.NewString()
	first, err := activate("v1.0", "0", requestID)
	if err != nil {
		t.Fatalf("activate v1.0: %v", err)
	}
	if first.AppliedRevision != 1 {
		t.Fatalf("first applied revision = %d, want 1", first.AppliedRevision)
	}
	later, err := activate("v1.1", "1", uuid.NewString())
	if err != nil {
		t.Fatalf("activate v1.1: %v", err)
	}
	if later.AppliedRevision != 2 {
		t.Fatalf("second applied revision = %d, want 2", later.AppliedRevision)
	}

	replay, err := activate("v1.0", "0", requestID)
	if err != nil {
		t.Fatalf("replay must succeed: %v", err)
	}
	if !replay.Replayed || replay.AppliedRevision != first.AppliedRevision {
		t.Fatalf("replay must return original applied revision: %+v", replay)
	}
	if replay.Active == nil || replay.Active.Version != "v1.1" || replay.Active.Revision != 2 {
		t.Fatalf("replay must report latest active state: %+v", replay.Active)
	}
	// 状态不得被重放改回 v1.0
	runtimeData, err := svc.RuntimeRelease(ctx, "qa_prod", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeData.Version != "v1.1" {
		t.Fatalf("replay must not switch back, active version = %s", runtimeData.Version)
	}
}

// TestReleaseActiveReadDuringSwitchKeepsOldSnapshot R17:读取途中完成切换,
// 旧在途响应内部必须保持一致(版本、URL、资源字节都属于旧快照),随后新请求读到新版本。
func TestReleaseActiveReadDuringSwitchKeepsOldSnapshot(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()
	if _, err := fixture.create(t, "v1.0", "1", ""); err != nil {
		t.Fatalf("create v1.0: %v", err)
	}
	if _, err := fixture.create(t, "v1.1", "1", ""); err != nil {
		t.Fatalf("create v1.1: %v", err)
	}
	if _, err := svc.ActivateRelease(ctx, ReleaseActivateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedActiveRevisionRaw: "0", RequestID: uuid.NewString(), Reason: "R17 基线",
	}); err != nil {
		t.Fatalf("activate v1.0: %v", err)
	}

	paused := make(chan string, 1)
	// Use a distinct historical timestamp so a later switch within the same
	// second cannot hide an accidental second head lookup.
	oldActivatedAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := svc.logic.db.Model(&model.ConfigReleaseHead{}).
		Where("environment_key = ? AND pipeline_key = ?", "qa_prod", "main").
		Update("changed_at", oldActivatedAt).Error; err != nil {
		t.Fatal(err)
	}
	activeBefore, err := svc.ReleaseActive(ctx, "qa_prod", "main")
	if err != nil || activeBefore == nil {
		t.Fatalf("active before read: %v", err)
	}
	resume := make(chan struct{})
	releaseReadPauseHookForTest = func(snapshotID string) {
		paused <- snapshotID
		<-resume
	}
	t.Cleanup(func() { releaseReadPauseHookForTest = nil })

	type readResult struct {
		data *ReleaseRuntimeConfig
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		data, err := svc.RuntimeRelease(adminTestContext(), "qa_prod", "main", nil)
		done <- readResult{data: data, err: err}
	}()

	oldSnapshot := <-paused
	// 在旧请求"已读到 head、尚未读取快照"的窗口内完成切换
	if _, err := svc.ActivateRelease(ctx, ReleaseActivateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.1",
		ExpectedActiveRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "R17 途中切换",
	}); err != nil {
		t.Fatalf("activate during read: %v", err)
	}
	close(resume)

	inFlight := <-done
	if inFlight.err != nil {
		t.Fatalf("in-flight read failed: %v", inFlight.err)
	}
	if inFlight.data.SnapshotID != oldSnapshot || inFlight.data.Version != "v1.0" {
		t.Fatalf("in-flight read must stay on the old snapshot: %+v", inFlight.data)
	}
	if inFlight.data.ActiveRevision == nil || *inFlight.data.ActiveRevision != 1 {
		t.Fatalf("in-flight read must report the revision it resolved: %+v", inFlight.data.ActiveRevision)
	}
	if inFlight.data.ActivatedAt != activeBefore.ChangedAt {
		t.Fatalf("in-flight timestamp must belong to old head: %s != %s", inFlight.data.ActivatedAt, activeBefore.ChangedAt)
	}
	oldAsset := findRuntimeConfig(t, inFlight.data, "REL_IMAGE")
	oldURL := strings.TrimSpace(oldAsset.Content)
	if !strings.Contains(oldURL, "/api/v1/release/asset/"+oldSnapshot+"/") {
		t.Fatalf("asset URL must pin the old snapshot: %s", oldURL)
	}
	_, oldBytes, err := svc.ReleaseAssetDownload(ctx, oldSnapshot, releaseAssetIDFromURL(t, oldURL))
	if err != nil {
		t.Fatalf("old asset must stay downloadable: %v", err)
	}
	if string(oldBytes) != "PNG-A-BYTES" {
		t.Fatalf("old asset bytes changed: %q", string(oldBytes))
	}

	// 切换后的新请求读到 v1.1
	after, err := svc.RuntimeRelease(ctx, "qa_prod", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != "v1.1" || after.SnapshotID == oldSnapshot {
		t.Fatalf("new request must read the new snapshot: %+v", after)
	}
	if after.ActivatedAt == "" || after.ActivatedAt == inFlight.data.ActivatedAt {
		t.Fatalf("new request must carry the new activation time: %+v", after)
	}
}

// --------------------- R13:复制期间并发写入 ---------------------

// TestReleaseCreateDuringConcurrentWrites R13:复制资源期间同时解锁、改配置、重新锁版,
// 快照必须只来自创建时的那一个冻结 revision;其他写要么排队成功、要么有界失败,不得混版。
func TestReleaseCreateDuringConcurrentWrites(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()

	rows, err := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var textKey string
	for _, cfg := range rows {
		if cfg.Alias == "REL_TEXT" {
			textKey = cfg.ResourceKey
		}
	}
	if textKey == "" {
		t.Fatal("REL_TEXT fixture config missing")
	}

	paused := make(chan struct{}, 1)
	resume := make(chan struct{})
	releaseCopyPauseHookForTest = func(string) {
		select {
		case paused <- struct{}{}:
		default:
		}
		<-resume
	}
	t.Cleanup(func() { releaseCopyPauseHookForTest = nil })

	type outcome struct {
		label string
		err   error
	}
	outcomes := make(chan outcome, 3)
	createDone := make(chan error, 1)
	go func() {
		_, err := svc.CreateRelease(adminTestContext(), ReleaseCreateInput{
			EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
			ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "R13 复制中并发",
		})
		createDone <- err
	}()
	select {
	case <-paused:
	case err := <-createDone:
		t.Fatalf("create finished before copy pause: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("copy pause hook never fired")
	}

	// 复制暂停期间发起三种写:解锁、改配置、重新锁版(锁版排在解锁之后)
	unlocked := make(chan struct{})
	go func() {
		_, err := svc.UnlockFreeze(adminTestContext(), "qa_prod", "main", "R13 解锁", "1")
		outcomes <- outcome{"unlock", err}
		close(unlocked)
	}()
	go func() {
		updated := buildConfig("qa_prod", "main", "REL_TEXT", "text", "api=https://api.example.com/v1/after")
		updated.ResourceKey = textKey
		_, err := svc.UpdateConfig(adminTestContext(), updated, currentEditRevision(t, svc, updated))
		outcomes <- outcome{"update", err}
	}()
	go func() {
		<-unlocked
		_, err := svc.LockFreeze(adminTestContext(), "qa_prod", "main", "R13 重新锁版", "2")
		outcomes <- outcome{"lock", err}
	}()
	time.Sleep(150 * time.Millisecond)
	close(resume)

	if err := <-createDone; err != nil {
		t.Fatalf("create must succeed while writers wait: %v", err)
	}
	collected := map[string]error{}
	for i := 0; i < 3; i++ {
		result := <-outcomes
		collected[result.label] = result.err
	}
	// 写协调保证有界:要么排队完成,要么明确失败(503 等待不可用 / 409 状态冲突),不允许静默丢弃
	for label, err := range collected {
		if err == nil {
			t.Logf("concurrent writer %s succeeded", label)
			continue
		}
		t.Logf("concurrent writer %s rejected: %v", label, err)
		if releaseErr, ok := AsReleaseError(err); ok {
			t.Fatalf("writer %s failed with release error: %v", label, releaseErr.Message)
		}
		if freezeErr, ok := AsFreezeError(err); ok {
			if freezeErr.HTTPStatus != 503 && freezeErr.HTTPStatus != 409 {
				t.Fatalf("writer %s must fail with bounded 503/409, got %d/%s", label, freezeErr.HTTPStatus, freezeErr.ErrorType)
			}
			continue
		}
		t.Fatalf("writer %s unexpected error: %v", label, err)
	}

	// 快照内容必须来自创建时的锁定 revision(旧内容),不被并发修改污染
	row, err := svc.logic.releaseDAO.GetVersionByScopeAndName(ctx, svc.logic.db, "qa_prod", "main", "v1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row.Payload, "api.example.com/v1/endpoint") {
		t.Fatalf("snapshot must keep the frozen revision content: %s", row.Payload)
	}
	if strings.Contains(row.Payload, "/after") {
		t.Fatalf("snapshot must not contain the concurrent edit: %s", row.Payload)
	}
	if row.SourceFreezeRevision != 1 {
		t.Fatalf("source freeze revision = %d, want 1", row.SourceFreezeRevision)
	}

	// 编辑区一致性:并发写成功才允许内容变化;被冻结写拦截时内容必须保持不变
	after, err := svc.logic.ListConfigs(ctx, "qa_prod", "main", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	foundAfter := false
	for _, cfg := range after {
		if cfg.Alias == "REL_TEXT" && strings.Contains(cfg.Content, "/after") {
			foundAfter = true
		}
	}
	if collected["update"] == nil && !foundAfter {
		t.Fatal("editor must reflect a successful concurrent config update")
	}
	if collected["update"] != nil && foundAfter {
		t.Fatal("editor must not change when the concurrent update was rejected")
	}
	status, err := svc.FreezeStatus(ctx, "qa_prod", "main")
	if err != nil {
		t.Fatal(err)
	}
	if collected["unlock"] == nil && collected["lock"] == nil && status.Revision < 3 {
		t.Fatalf("freeze revision must advance past the concurrent lock cycle, got %d", status.Revision)
	}
	if status.Revision == 0 {
		t.Fatalf("freeze revision must never go backwards, got %d", status.Revision)
	}
}

// releaseAssetIDFromURL 从渲染后的资源地址中取出 asset_id。
func releaseAssetIDFromURL(t *testing.T, value string) string {
	t.Helper()
	marker := "/api/v1/release/asset/"
	index := strings.Index(value, marker)
	if index < 0 {
		t.Fatalf("asset url missing marker: %s", value)
	}
	rest := value[index+len(marker):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		t.Fatalf("asset url malformed: %s", value)
	}
	id := strings.SplitN(parts[1], "?", 2)[0]
	id = strings.SplitN(id, "#", 2)[0]
	return id
}

// --------------------- F5:已提交结果的幂等确认不依赖当前配置 ---------------------

// TestReleaseReplayIndependentOfSourceChanges 复核 F5:源存储改为 MinIO 或快照根目录不可用时,已提交结果仍可确认。
func TestReleaseReplayIndependentOfSourceChanges(t *testing.T) {
	fixture := newReleaseFixture(t)
	svc := fixture.svc
	ctx := adminTestContext()

	input := ReleaseCreateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedFreezeRevisionRaw: "1", RequestID: uuid.NewString(), Reason: "存储变更前创建",
	}
	first, err := svc.CreateRelease(ctx, input)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Replaying a committed request must bypass even a now-unavailable MinIO source.
	svc.config = &config.Config{Storage: config.StorageConfig{Type: "minio"}}
	replay, err := svc.CreateRelease(ctx, input)
	if err != nil {
		t.Fatalf("replay must survive source storage change: %v", err)
	}
	if !replay.Replayed || replay.Meta.SnapshotID != first.Meta.SnapshotID {
		t.Fatalf("replay must return the committed snapshot: %+v", replay)
	}

	// 快照根目录不可用(相对路径)时,确认已提交结果同样不应受前置副作用影响
	svc.config = nil
	t.Setenv(releaseDirEnv, "relative/release-dir")
	replay2, err := svc.CreateRelease(ctx, input)
	if err != nil {
		t.Fatalf("replay must not depend on snapshot root init: %v", err)
	}
	if !replay2.Replayed || replay2.Meta.SnapshotID != first.Meta.SnapshotID {
		t.Fatalf("replay must return the committed snapshot: %+v", replay2)
	}
	newOne := input
	newOne.RequestID = uuid.NewString()
	newOne.Version = "v1.2"
	if _, err := svc.CreateRelease(ctx, newOne); err == nil {
		t.Fatal("new create with unusable snapshot root must fail")
	} else {
		requireReleaseErrorType(t, err, ErrTypeReleaseStorageError)
	}

	// 激活的幂等确认同样不依赖快照目录:切到别的版本后再重放原切换请求
	t.Setenv(releaseDirEnv, "")
	activated, err := svc.ActivateRelease(ctx, ReleaseActivateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedActiveRevisionRaw: "0", RequestID: uuid.NewString(), Reason: "切换 v1.0",
	})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	t.Setenv(releaseDirEnv, "relative/release-dir")
	activateReplay, err := svc.ActivateRelease(ctx, ReleaseActivateInput{
		EnvironmentKey: "qa_prod", PipelineKey: "main", Version: "v1.0",
		ExpectedActiveRevisionRaw: "0", RequestID: activated.RequestID, Reason: "切换 v1.0",
	})
	if err != nil {
		t.Fatalf("activate replay must survive snapshot root change: %v", err)
	}
	if !activateReplay.Replayed || activateReplay.AppliedRevision != activated.AppliedRevision {
		t.Fatalf("activate replay must return original revision: %+v", activateReplay)
	}
}
