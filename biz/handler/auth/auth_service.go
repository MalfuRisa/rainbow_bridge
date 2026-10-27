// 手写 handler:认证契约(JSON、Cookie、CSRF 与错误映射)。生成的路由指向本文件入口。
package auth

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/yi-nology/rainbow_bridge/biz/handler"
	"github.com/yi-nology/rainbow_bridge/biz/service"
)

var svc *service.Service

func SetService(s *service.Service) {
	svc = s
}

// authRequestBodyLimit 是登录/改密请求体上限;超过返回 413。
const authRequestBodyLimit = 4 * 1024

// --------------------- 响应契约 ---------------------

type authUserJSON struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type authSessionJSON struct {
	ExpiresAt     string `json:"expires_at"`
	IdleExpiresAt string `json:"idle_expires_at"`
}

type authSessionDataJSON struct {
	User      authUserJSON    `json:"user"`
	Session   authSessionJSON `json:"session"`
	CSRFToken string          `json:"csrf_token"`
}

type authSessionEnvelopeJSON struct {
	Code int32               `json:"code"`
	Msg  string              `json:"msg"`
	Data authSessionDataJSON `json:"data"`
}

type authLogoutDataJSON struct {
	LoggedOut bool `json:"logged_out"`
}

type authLogoutEnvelopeJSON struct {
	Code int32              `json:"code"`
	Msg  string             `json:"msg"`
	Data authLogoutDataJSON `json:"data"`
}

type authPasswordDataJSON struct {
	ReauthenticationRequired bool `json:"reauth_required"`
}

type authPasswordEnvelopeJSON struct {
	Code int32                `json:"code"`
	Msg  string               `json:"msg"`
	Data authPasswordDataJSON `json:"data"`
}

type authErrorJSON struct {
	Code      int32  `json:"code"`
	Msg       string `json:"msg"`
	ErrorType string `json:"error_type"`
}

func writeAuthError(c *app.RequestContext, err error) {
	authErr, ok := service.AsAuthError(err)
	if !ok {
		authErr = service.AuthUnavailableError(err)
	}
	c.Header("Cache-Control", "no-store")
	if authErr.HTTPStatus == consts.StatusTooManyRequests && authErr.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(authErr.RetryAfter))
	}
	c.JSON(authErr.HTTPStatus, authErrorJSON{
		Code:      int32(authErr.Code),
		Msg:       authErr.Error(),
		ErrorType: authErr.ErrorType,
	})
}

// --------------------- 请求解析 ---------------------

// requestOrigin 返回 Origin 头与是否存在。
func requestOrigin(c *app.RequestContext) (string, bool) {
	raw := c.Request.Header.Peek("Origin")
	if len(raw) == 0 {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}

// requestSessionToken 读取会话 Cookie(凭证只在请求内存中使用)。
func requestSessionToken(c *app.RequestContext) string {
	settings := svc.AuthSettings()
	value := c.Cookie(settings.CookieName)
	if len(value) == 0 {
		return ""
	}
	return string(value)
}

// clientIP 只使用连接来源地址;不解析 X-Forwarded-For,避免伪造绕过限流。
func clientIP(c *app.RequestContext) string {
	address := c.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}
	return address
}

// decodeAuthJSON 强制 application/json 与请求体上限,并拒绝未知字段。
func decodeAuthJSON(c *app.RequestContext, target any) error {
	contentType := strings.ToLower(strings.TrimSpace(string(c.Request.Header.ContentType())))
	if !strings.HasPrefix(contentType, "application/json") {
		return service.AuthUnsupportedMediaError()
	}
	if length := c.Request.Header.ContentLength(); length > authRequestBodyLimit {
		return service.AuthRequestTooLargeError()
	}
	body := c.Request.Body()
	if len(body) > authRequestBodyLimit {
		return service.AuthRequestTooLargeError()
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if err == io.EOF {
			return service.AuthInvalidRequestError("请求体不能为空")
		}
		return service.AuthInvalidRequestError("请求体不是合法 JSON")
	}
	return nil
}

// setSessionCookie 设置会话 Cookie(SameSite=Lax、HttpOnly,Secure 由配置决定)。
func setSessionCookie(c *app.RequestContext, cookieValue *http.Cookie) {
	// 用标准库格式化 Set-Cookie,保证 Path/HttpOnly/Secure/SameSite/Max-Age 与删除时一致
	c.Response.Header.Set("Set-Cookie", cookieValue.String())
}

// clearSessionCookie 使用与设置时完全相同的属性删除 Cookie。
func clearSessionCookie(c *app.RequestContext) {
	setSessionCookie(c, service.BuildExpiredSessionCookie(svc.AuthSettings()))
}

// sessionPayload 把服务层结果转换成响应数据结构。
func sessionPayload(user service.AuthUserView, session service.AuthSessionView, csrf string) authSessionDataJSON {
	return authSessionDataJSON{
		User:      authUserJSON{ID: strconv.FormatInt(user.ID, 10), Username: user.Username, Role: user.Role},
		Session:   authSessionJSON{ExpiresAt: session.ExpiresAt.Format(time.RFC3339), IdleExpiresAt: session.IdleExpiresAt.Format(time.RFC3339)},
		CSRFToken: csrf,
	}
}

// --------------------- 接口 ---------------------

// Login .
// @router /api/v1/auth/login [POST]
func Login(ctx context.Context, c *app.RequestContext) {
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeAuthJSON(c, &request); err != nil {
		writeAuthError(c, err)
		return
	}
	origin, hasOrigin := requestOrigin(c)
	result, err := svc.Login(handler.EnrichContext(ctx, c), service.AuthLoginInput{
		Username:     request.Username,
		Password:     request.Password,
		ClientIP:     clientIP(c),
		Origin:       origin,
		HasOrigin:    hasOrigin,
		SessionToken: requestSessionToken(c),
	})
	if err != nil {
		writeAuthError(c, err)
		return
	}
	settings := svc.AuthSettings()
	setSessionCookie(c, service.BuildSessionCookie(settings, result.Token, result.Session.ExpiresAt, time.Now().UTC()))
	c.Header("Cache-Control", "no-store")
	c.JSON(consts.StatusOK, authSessionEnvelopeJSON{Code: 0, Msg: "success", Data: sessionPayload(result.User, result.Session, result.CSRFToken)})
}

// Me .
// @router /api/v1/auth/me [GET]
func Me(ctx context.Context, c *app.RequestContext) {
	origin, hasOrigin := requestOrigin(c)
	result, err := svc.CurrentSession(handler.EnrichContext(ctx, c), service.AuthRequestInput{
		SessionToken: requestSessionToken(c),
		Origin:       origin,
		HasOrigin:    hasOrigin,
	})
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(consts.StatusOK, authSessionEnvelopeJSON{Code: 0, Msg: "success", Data: sessionPayload(result.User, result.Session, result.CSRFToken)})
}

// Logout .
// @router /api/v1/auth/logout [POST]
func Logout(ctx context.Context, c *app.RequestContext) {
	if err := consumeEmptyJSONBody(c); err != nil {
		writeAuthError(c, err)
		return
	}
	origin, hasOrigin := requestOrigin(c)
	err := svc.Logout(handler.EnrichContext(ctx, c), service.AuthRequestInput{
		SessionToken: requestSessionToken(c),
		CSRFToken:    strings.TrimSpace(string(c.GetHeader("X-CSRF-Token"))),
		Origin:       origin,
		HasOrigin:    hasOrigin,
	})
	if err != nil {
		// 数据库不可用时不声称已完成服务端退出
		writeAuthError(c, err)
		return
	}
	clearSessionCookie(c)
	c.Header("Cache-Control", "no-store")
	c.JSON(consts.StatusOK, authLogoutEnvelopeJSON{Code: 0, Msg: "success", Data: authLogoutDataJSON{LoggedOut: true}})
}

// Password .
// @router /api/v1/auth/password [POST]
func Password(ctx context.Context, c *app.RequestContext) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeAuthJSON(c, &request); err != nil {
		writeAuthError(c, err)
		return
	}
	origin, hasOrigin := requestOrigin(c)
	err := svc.ChangePassword(handler.EnrichContext(ctx, c), service.AuthPasswordChangeInput{
		AuthRequestInput: service.AuthRequestInput{
			SessionToken: requestSessionToken(c),
			CSRFToken:    strings.TrimSpace(string(c.GetHeader("X-CSRF-Token"))),
			Origin:       origin,
			HasOrigin:    hasOrigin,
		},
		CurrentPassword: request.CurrentPassword,
		NewPassword:     request.NewPassword,
	})
	if err != nil {
		writeAuthError(c, err)
		return
	}
	clearSessionCookie(c)
	c.Header("Cache-Control", "no-store")
	c.JSON(consts.StatusOK, authPasswordEnvelopeJSON{Code: 0, Msg: "success", Data: authPasswordDataJSON{ReauthenticationRequired: true}})
}

// consumeEmptyJSONBody 允许 {} 与空体,拒绝其他内容。
func consumeEmptyJSONBody(c *app.RequestContext) error {
	body := strings.TrimSpace(string(c.Request.Body()))
	if body == "" || body == "{}" {
		return nil
	}
	return service.AuthInvalidRequestError("该接口不接受业务参数")
}