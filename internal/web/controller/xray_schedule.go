package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const geodataScheduleSaveTimeout = 4 * time.Minute

// GeodataScheduleTimeout must run before gzip wraps the response writer, so
// ResponseController can reach the connection's write deadline.
func GeodataScheduleTimeout(basePath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost || c.Request.URL.Path != basePath+"panel/api/xray/schedule/geodata" {
			return
		}
		// Leave a minute after the operation budget to return errors or apply results.
		if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(geodataScheduleSaveTimeout + time.Minute)); err != nil {
			jsonObj(c, nil, err)
			c.Abort()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), geodataScheduleSaveTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

type XrayScheduleController struct {
	scheduler *service.XrayScheduler
	settings  service.XraySettingService
	xray      service.XrayService
}

func NewXrayScheduleController(g *gin.RouterGroup, scheduler *service.XrayScheduler) *XrayScheduleController {
	a := &XrayScheduleController{scheduler: scheduler}
	g = g.Group("/xray/schedule")
	g.GET("/restart", a.getRestart)
	g.POST("/restart", a.saveRestart)
	g.POST("/restart/run", a.runRestart)
	g.GET("/geodata", a.getGeodata)
	g.POST("/geodata", a.saveGeodata)
	g.GET("/logs", a.getLogs)
	g.POST("/logs/settings", a.saveLogSettings)
	g.POST("/logs/clear", a.clearLogs)
	return a
}

func (a *XrayScheduleController) getLogs(c *gin.Context) {
	view, err := service.GetTaskExecutionLogs()
	jsonObj(c, view, err)
}

func (a *XrayScheduleController) saveLogSettings(c *gin.Context) {
	var cfg service.TaskLogSettings
	if err := c.ShouldBindJSON(&cfg); err != nil {
		pureJsonMsg(c, http.StatusBadRequest, false, err.Error())
		return
	}
	if err := service.SaveTaskLogSettings(cfg); err != nil {
		jsonObj(c, nil, err)
		return
	}
	a.getLogs(c)
}

func (a *XrayScheduleController) clearLogs(c *gin.Context) {
	if err := service.ClearTaskExecutionLogs(); err != nil {
		jsonObj(c, nil, err)
		return
	}
	a.getLogs(c)
}

func (a *XrayScheduleController) schedulerReady(c *gin.Context) bool {
	if a.scheduler != nil {
		return true
	}
	jsonObj(c, nil, errors.New("Xray task scheduler is not available"))
	return false
}

func (a *XrayScheduleController) getRestart(c *gin.Context) {
	if !a.schedulerReady(c) {
		return
	}
	view, err := a.scheduler.View()
	jsonObj(c, view, err)
}

func (a *XrayScheduleController) saveRestart(c *gin.Context) {
	if !a.schedulerReady(c) {
		return
	}
	var cfg service.XrayRestartSchedule
	if err := c.ShouldBindJSON(&cfg); err != nil {
		pureJsonMsg(c, http.StatusBadRequest, false, err.Error())
		return
	}
	if err := a.scheduler.Save(cfg); err != nil {
		jsonObj(c, nil, err)
		return
	}
	view, err := a.scheduler.View()
	jsonObj(c, view, err)
}

func (a *XrayScheduleController) runRestart(c *gin.Context) {
	if !a.schedulerReady(c) {
		return
	}
	run, err := a.scheduler.RunNow()
	jsonObj(c, run, err)
}

func (a *XrayScheduleController) getGeodata(c *gin.Context) {
	view, err := a.settings.GetGeodataSchedule()
	jsonObj(c, view, err)
}

func (a *XrayScheduleController) saveGeodata(c *gin.Context) {
	var cfg service.GeodataUpdateSchedule
	if err := c.ShouldBindJSON(&cfg); err != nil {
		pureJsonMsg(c, http.StatusBadRequest, false, err.Error())
		return
	}
	if err := a.settings.SaveGeodataSchedule(c.Request.Context(), cfg); err != nil {
		var scheduleErr *service.GeodataScheduleError
		if errors.As(err, &scheduleErr) {
			jsonMsg(c, I18nWeb(c, "pages.xray.schedule."+scheduleErr.Key, "file=="+scheduleErr.File), err)
			return
		}
		jsonObj(c, nil, err)
		return
	}
	// Application failures are distinct from saves; a stopped core loads its
	// plan on next start, and a failed application must remain visible.
	applied, applyErr := a.xray.RestartXrayIfRunning(c.Request.Context())
	view, err := a.settings.GetGeodataSchedule()
	if view != nil {
		view.Applied = applied && applyErr == nil
		if applyErr != nil {
			view.ApplyError = applyErr.Error()
		}
	}
	jsonObj(c, view, err)
}
