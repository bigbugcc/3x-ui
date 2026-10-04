package controller

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

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
	return a
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
	if err := a.settings.SaveGeodataSchedule(cfg); err != nil {
		jsonObj(c, nil, err)
		return
	}
	// Application failures are distinct from saves; a stopped core loads its
	// plan on next start, and a failed application must remain visible.
	applied, applyErr := a.xray.RestartXrayIfRunning()
	view, err := a.settings.GetGeodataSchedule()
	if view != nil {
		view.Applied = applied && applyErr == nil
		if applyErr != nil {
			view.ApplyError = applyErr.Error()
		}
	}
	jsonObj(c, view, err)
}
