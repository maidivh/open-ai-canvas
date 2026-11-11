package handler

import (
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterWantraceAssetRoutes 挂载「橙幻-火山方舟-海外版」素材库接口。
// 用户只能看到自己的认证会话和素材组；管理员配置与渠道探测在 service 校验。
func RegisterWantraceAssetRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/wantrace-assets", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		overview, err := svc.WantraceAssetOverview(user)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		ok(c, overview)
	})
	r.POST("/wantrace-assets/verifications", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "wantrace-verification:"+user.ID) {
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		verification, err := svc.CreateWantraceVerification(c.Request.Context(), user, req.Name)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		ok(c, gin.H{"verification": verification})
	})
	r.POST("/wantrace-assets/verifications/:id/check", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "wantrace-verification-check:"+user.ID) {
			return
		}
		verification, err := svc.CheckWantraceVerification(c.Request.Context(), user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		ok(c, gin.H{"verification": verification})
	})
	r.POST("/wantrace-assets/groups/:id/assets", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "wantrace-asset:"+user.ID) {
			return
		}
		var req struct {
			ResourceID string `json:"resourceId"`
			Name       string `json:"name"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		asset, err := svc.CreateWantraceAsset(c.Request.Context(), user, c.Param("id"), req.ResourceID, req.Name)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"asset": asset})
	})
	r.POST("/wantrace-assets/assets/:id/refresh", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "wantrace-asset-refresh:"+user.ID) {
			return
		}
		asset, err := svc.RefreshWantraceAsset(c.Request.Context(), user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"asset": asset})
	})
	r.POST("/wantrace-assets/assets/:id/library", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		assetID, err := svc.AddWantraceAssetToLibrary(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"assetId": assetID})
	})
	registerAdminWantraceAssetRoutes(r, svc)
}

func registerAdminWantraceAssetRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/settings/wantrace-assets", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		setting, err := svc.AdminWantraceAssetSetting(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
	r.PATCH("/admin/settings/wantrace-assets", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.WantraceAssetSettingRequest
		if !bindArkJSON(c, &req) {
			return
		}
		setting, err := svc.UpdateWantraceAssetSetting(c.Request.Context(), user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
	// Key 可能刚填写且尚未保存，用 POST 放在请求体里，避免进入访问日志和浏览器 URL。
	r.POST("/admin/settings/wantrace-assets/channels", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			APIKey string `json:"apiKey"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		channels, err := svc.AdminWantraceChannels(c.Request.Context(), user, req.APIKey)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"channels": channels})
	})
	r.POST("/admin/settings/wantrace-assets/test", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		setting, err := svc.TestAdminWantraceAssetSetting(c.Request.Context(), user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, setting)
	})
}
