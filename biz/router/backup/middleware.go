// Hand-written middleware hooks matching the hertz generated router layout.

package backup

import "github.com/cloudwego/hertz/pkg/app"

func rootMw() []app.HandlerFunc    { return nil }
func _apiMw() []app.HandlerFunc    { return nil }
func _v1Mw() []app.HandlerFunc     { return nil }
func _backupMw() []app.HandlerFunc { return nil }
