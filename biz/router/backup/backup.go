// Hand-written route registration for the backup module. The layout mirrors
// the hertz generator output used by the other modules.

package backup

import (
	"github.com/cloudwego/hertz/pkg/app/server"
	backuphandler "github.com/yi-nology/rainbow_bridge/biz/handler/backup"
)

// Register registers the whole-instance backup management routes.
func Register(r *server.Hertz) {
	root := r.Group("/", rootMw()...)
	{
		_api := root.Group("/api", _apiMw()...)
		{
			_v1 := _api.Group("/v1", _v1Mw()...)
			{
				_backup := _v1.Group("/backup", _backupMw()...)
				_backup.GET("/capabilities", backuphandler.Capabilities)
				_backup.POST("/create", backuphandler.Create)
				_backup.GET("/list", backuphandler.List)
				_backup.GET("/status", backuphandler.Status)
				_backup.GET("/download", backuphandler.Download)
				_backup.POST("/delete", backuphandler.Delete)
				_backup.POST("/restore/preview", backuphandler.PreviewUpload)
				_backup.GET("/restore/preview", backuphandler.Preview)
				_backup.POST("/restore/execute", backuphandler.Execute)
				_backup.POST("/restore/cancel", backuphandler.Cancel)
			}
		}
	}
}
