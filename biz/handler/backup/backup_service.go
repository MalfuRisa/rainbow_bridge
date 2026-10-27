package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	backupcore "github.com/yi-nology/rainbow_bridge/biz/backup"
	"github.com/yi-nology/rainbow_bridge/biz/model/backup"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

var svc *service.Service

func SetService(s *service.Service) {
	svc = s
}

const maxJSONBody = 1 << 20

// --------------------- responses ---------------------

func writeData(c *app.RequestContext, status int, code int32, data any) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, &backup.Envelope{Code: code, Msg: "OK", Data: data})
}

func writeJob(c *app.RequestContext, status int, code int32, job *backupcore.Job) {
	writeData(c, status, code, &backup.JobData{Job: job})
}

func writeError(c *app.RequestContext, err error) {
	classified, ok := backupcore.AsError(err)
	if !ok {
		log.Printf("[backup] internal error: %v", err)
		classified = &backupcore.Error{
			HTTPStatus: http.StatusInternalServerError,
			Type:       backupcore.ErrInternal,
			Message:    "internal backup error",
		}
	}
	c.Header("Cache-Control", "no-store")
	if classified.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(classified.RetryAfter))
	}
	c.JSON(classified.HTTPStatus, &backup.Envelope{
		Code:      int32(classified.HTTPStatus),
		Msg:       classified.Message,
		Error:     classified.Message,
		ErrorType: classified.Type,
	})
}

func writeUnsupported(c *app.RequestContext) {
	caps := svc.BackupCapabilities()
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusServiceUnavailable, &backup.Envelope{
		Code:      http.StatusServiceUnavailable,
		Msg:       "backup is not supported in this deployment",
		Error:     "backup is not supported in this deployment",
		ErrorType: backupcore.ErrUnsupported,
		Data:      caps,
	})
}

func actorFrom(ctx context.Context) string {
	identity, ok := service.AdminIdentityFrom(ctx)
	if !ok {
		return "admin"
	}
	return identity.Username
}

// decodeJSON enforces JSON content, a bounded body and no unknown fields.
func decodeJSON(c *app.RequestContext, target any) error {
	contentType := strings.ToLower(strings.TrimSpace(string(c.Request.Header.ContentType())))
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return &backupcore.Error{HTTPStatus: http.StatusBadRequest,
			Type: backupcore.ErrInvalidRequest, Message: "Content-Type must be application/json"}
	}
	if length := c.Request.Header.ContentLength(); length > maxJSONBody {
		return &backupcore.Error{HTTPStatus: http.StatusBadRequest,
			Type: backupcore.ErrInvalidRequest, Message: "request body is too large"}
	}
	var body []byte
	if c.Request.IsBodyStream() {
		body, err = io.ReadAll(io.LimitReader(c.Request.BodyStream(), maxJSONBody+1))
		if err != nil {
			return &backupcore.Error{HTTPStatus: http.StatusBadRequest,
				Type: backupcore.ErrInvalidRequest, Message: "request body is unreadable"}
		}
	} else {
		body = c.Request.Body()
	}
	if len(body) == 0 || len(body) > maxJSONBody {
		return &backupcore.Error{HTTPStatus: http.StatusBadRequest,
			Type: backupcore.ErrInvalidRequest, Message: "request body is missing or too large"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &backupcore.Error{HTTPStatus: http.StatusBadRequest,
			Type: backupcore.ErrInvalidRequest, Message: "request body is not valid JSON: " + err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &backupcore.Error{HTTPStatus: http.StatusBadRequest,
			Type: backupcore.ErrInvalidRequest, Message: "request body must contain one JSON object"}
	}
	return nil
}

// Preview returns a validated package preview.
// @router /api/v1/backup/restore/preview [GET]
func Preview(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	preview, err := manager.GetPreview(c.Query("preview_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	writeData(c, http.StatusOK, http.StatusOK, preview)
}

// Execute prepares the validated package for the next service restart.
// @router /api/v1/backup/restore/execute [POST]
func Execute(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	request := &backup.ExecuteRequest{}
	if err := decodeJSON(c, request); err != nil {
		writeError(c, err)
		return
	}
	job, err := manager.ExecuteRestore(ctx, backupcore.ExecuteInput{
		PreviewID:      request.PreviewID,
		PackageSHA256:  request.PackageSha256,
		TargetInstance: request.TargetInstanceID,
		RequestID:      request.RequestID,
		Confirmation:   request.Confirmation,
		Reason:         request.Reason,
		Actor:          actorFrom(ctx),
	})
	if err != nil {
		writeError(c, err)
		return
	}
	writeJob(c, http.StatusAccepted, http.StatusAccepted, job)
}

// Cancel abandons a prepared restore before the restart.
// @router /api/v1/backup/restore/cancel [POST]
func Cancel(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	request := &backup.CancelRequest{}
	if err := decodeJSON(c, request); err != nil {
		writeError(c, err)
		return
	}
	if _, err := manager.CancelRestore(ctx, request.JobID, request.RequestID, actorFrom(ctx)); err != nil {
		writeError(c, err)
		return
	}
	writeData(c, http.StatusOK, http.StatusOK, &backup.CancelData{Cancelled: true})
}

// --------------------- endpoints ---------------------

// Capabilities returns the support statement even when unsupported.
// @router /api/v1/backup/capabilities [GET]
func Capabilities(ctx context.Context, c *app.RequestContext) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, &backup.Envelope{
		Code: http.StatusOK,
		Msg:  "OK",
		Data: svc.BackupCapabilities(),
	})
}

// Create starts an asynchronous backup.
// @router /api/v1/backup/create [POST]
func Create(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	request := &backup.CreateRequest{}
	if err := decodeJSON(c, request); err != nil {
		writeError(c, err)
		return
	}
	job, err := manager.CreateBackup(ctx, actorFrom(ctx), request.Reason, request.RequestID)
	if err != nil {
		writeError(c, err)
		return
	}
	writeJob(c, http.StatusAccepted, http.StatusAccepted, job)
}

// List returns downloadable completed backups.
// @router /api/v1/backup/list [GET]
func List(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(c, &backupcore.Error{HTTPStatus: 400, Type: backupcore.ErrInvalidRequest,
				Message: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	page, err := manager.ListBackups(limit, c.Query("before_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	writeData(c, http.StatusOK, http.StatusOK, &backup.ListData{
		Items: page.Items, HasMore: page.HasMore, NextBeforeID: page.NextBeforeID,
	})
}

// Status returns one task record, including terminal failures.
// @router /api/v1/backup/status [GET]
func Status(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	job, err := manager.JobStatus(c.Query("job_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	writeJob(c, http.StatusOK, http.StatusOK, job)
}

// Download streams a completed ZIP with its digest header.
// @router /api/v1/backup/download [GET]
func Download(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	download, err := manager.OpenDownload(c.Query("backup_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	defer download.Close()
	c.Header("Cache-Control", "no-store")
	c.Header("X-Rainbow-Backup-Sha256", download.Sha256)
	c.Header("Content-Length", strconv.FormatInt(download.Size, 10))
	c.FileAttachment(download.Path, download.FileName)
}

// Delete removes a completed backup after digest confirmation.
// @router /api/v1/backup/delete [POST]
func Delete(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	request := &backup.DeleteRequest{}
	if err := decodeJSON(c, request); err != nil {
		writeError(c, err)
		return
	}
	if err := manager.DeleteBackup(request.BackupID, request.ExpectedSha256, request.RequestID, actorFrom(ctx)); err != nil {
		writeError(c, err)
		return
	}
	writeData(c, http.StatusOK, http.StatusOK, &backup.DeleteData{Deleted: true})
}

// PreviewUpload streams a multipart package and starts validation.
// @router /api/v1/backup/restore/preview [POST]
func PreviewUpload(ctx context.Context, c *app.RequestContext) {
	manager := svc.BackupManager()
	if manager == nil {
		writeUnsupported(c)
		return
	}
	mediaType, params, err := mime.ParseMediaType(string(c.Request.Header.ContentType()))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		writeError(c, &backupcore.Error{HTTPStatus: 400, Type: backupcore.ErrInvalidRequest,
			Message: "Content-Type must be multipart/form-data"})
		return
	}
	reader := multipart.NewReader(c.Request.BodyStream(), params["boundary"])
	requestID, tempPath, cleanup, size, digest, existing, err := streamUpload(manager, reader)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		writeError(c, err)
		return
	}
	if existing != nil {
		writeJob(c, http.StatusAccepted, http.StatusAccepted, existing)
		return
	}
	job, err := manager.FinishUpload(ctx, actorFrom(ctx), requestID, tempPath, size, digest)
	if err != nil {
		writeError(c, err)
		return
	}
	writeJob(c, http.StatusAccepted, http.StatusAccepted, job)
}

func streamUpload(manager *backupcore.Manager, reader *multipart.Reader) (
	string, string, func(), int64, string, *backupcore.Job, error) {
	var (
		requestID string
		tempPath  string
		cleanup   func()
		size      int64
		digest    string
	)
	fail := func(err error) (string, string, func(), int64, string, *backupcore.Job, error) {
		return "", "", cleanup, 0, "", nil, err
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(&backupcore.Error{HTTPStatus: 400,
				Type: backupcore.ErrInvalidRequest, Message: "malformed multipart body"})
		}
		name := part.FormName()
		switch name {
		case "request_id":
			value, err := io.ReadAll(io.LimitReader(part, 128))
			_ = part.Close()
			if err != nil || len(value) == 0 {
				return fail(&backupcore.Error{HTTPStatus: 400,
					Type: backupcore.ErrInvalidRequest, Message: "request_id field is invalid"})
			}
			requestID = strings.TrimSpace(string(value))
		case "file":
			if requestID == "" {
				_ = part.Close()
				return fail(&backupcore.Error{HTTPStatus: 400,
					Type: backupcore.ErrInvalidRequest, Message: "request_id must precede the file part"})
			}
			existing, path, release, err := manager.BeginUpload(requestID)
			if err != nil {
				_ = part.Close()
				return fail(err)
			}
			if existing != nil {
				_ = part.Close()
				// Completed replay: return the original task without reading
				// a second package body.
				return "", "", release, 0, "", existing, nil
			}
			cleanup = release
			written, hash, err := writePart(part, path, manager.Limits().MaxPackageBytes)
			_ = part.Close()
			if err != nil {
				return fail(err)
			}
			tempPath, size, digest = path, written, hash
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1024))
			_ = part.Close()
		}
	}
	if tempPath == "" {
		return fail(&backupcore.Error{HTTPStatus: 400, Type: backupcore.ErrInvalidRequest,
			Message: "file part is required and request_id must precede it"})
	}
	return requestID, tempPath, nil, size, digest, nil, nil
}

func writePart(part *multipart.Part, path string, limit int64) (int64, string, error) {
	handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", &backupcore.Error{HTTPStatus: 500, Type: backupcore.ErrStorage,
			Message: "failed to create upload file"}
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(handle, hasher), io.LimitReader(part, limit+1))
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return 0, "", &backupcore.Error{HTTPStatus: 500, Type: backupcore.ErrStorage,
			Message: "failed to store uploaded package"}
	}
	if written > limit {
		_ = os.Remove(path)
		return 0, "", &backupcore.Error{HTTPStatus: 413, Type: backupcore.ErrLimitExceeded,
			Message: "uploaded package exceeds the size limit"}
	}
	return written, hex.EncodeToString(hasher.Sum(nil)), nil
}
